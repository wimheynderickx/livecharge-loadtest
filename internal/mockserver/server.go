package mockserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"os"
	"strings"
	"sync"
	"time"

	natsclient "github.com/nats-io/nats.go"

	"livecharge/loadtest/internal/config"
)

// MockServer hosts every configured endpoint behind a single transport.
//
// Lifecycle: NewMockServer parses the config and creates per-endpoint
// Handlers. Start opens the listener (NATS connection or HTTP server) and
// wires every Handler in. Stop closes the listener and waits for any
// in-flight handlers to return.
type MockServer struct {
	cfg      config.MockConfig
	handlers []*Handler

	// natsConn is non-nil for NATS mode.
	natsConn *natsclient.Conn
	subs     []*natsclient.Subscription

	// httpSrv is non-nil for HTTP mode.
	httpSrv *nethttp.Server

	// addr is the bound listener address after Start (host:port). Empty
	// before Start. Tests that bind to :0 read this to discover the port.
	addr string

	mu      sync.Mutex
	started bool
}

// NewMockServer builds Handlers for every endpoint and stores them. It
// does not yet open any sockets — that happens in Start.
func NewMockServer(cfg config.MockConfig) (*MockServer, error) {
	s := &MockServer{cfg: cfg}
	for i, ep := range cfg.Endpoints {
		h, err := NewHandler(ep)
		if err != nil {
			return nil, fmt.Errorf("endpoint[%d]: %w", i, err)
		}
		s.handlers = append(s.handlers, h)
	}
	return s, nil
}

// Start opens the network listener and registers every Handler. Start
// blocks only for the initial connection / bind; per-endpoint request
// handling happens in background goroutines or by the HTTP server's own
// connection pool.
func (s *MockServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("mock server already started")
	}

	switch s.cfg.Transport.Type {
	case "nats":
		if err := s.startNATS(); err != nil {
			return err
		}
	case "http", "https":
		if err := s.startHTTP(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported transport type %q for mock server", s.cfg.Transport.Type)
	}

	s.started = true
	return nil
}

// startNATS connects to the broker and subscribes each endpoint's subject.
// We use queue subscriptions so multiple mock instances could share the
// load (not the primary use case, but cheap to enable).
func (s *MockServer) startNATS() error {
	opts := []natsclient.Option{
		natsclient.Name("loadtest-mock"),
		natsclient.MaxReconnects(-1),
		natsclient.ReconnectWait(time.Second),
	}
	switch s.cfg.Transport.Auth.Type {
	case "", "none":
	case "userpass":
		opts = append(opts, natsclient.UserInfo(s.cfg.Transport.Auth.Username, s.cfg.Transport.Auth.Password))
	default:
		return fmt.Errorf("nats mock: unsupported auth type %q", s.cfg.Transport.Auth.Type)
	}

	conn, err := natsclient.Connect(s.cfg.Transport.URL, opts...)
	if err != nil {
		return fmt.Errorf("nats connect %s: %w", s.cfg.Transport.URL, err)
	}
	s.natsConn = conn

	for i, ep := range s.cfg.Endpoints {
		h := s.handlers[i]
		sub, err := conn.QueueSubscribe(ep.Subject, "loadtest-mock", func(msg *natsclient.Msg) {
			reply, noAnswer, _ := h.Handle(msg.Data)
			if noAnswer {
				// Simulate a server that received the request but never
				// replied. The client's request will time out per its
				// response_timeout.
				return
			}
			if msg.Reply != "" {
				_ = conn.Publish(msg.Reply, reply)
			}
		})
		if err != nil {
			return fmt.Errorf("nats subscribe %s: %w", ep.Subject, err)
		}
		s.subs = append(s.subs, sub)
	}
	return nil
}

// startHTTP builds an http.ServeMux with one handler per endpoint and
// starts an HTTP server in a background goroutine. TLS is enabled when
// transport.tls.enabled = true; the cert is loaded from disk if cert_file
// and key_file are set, otherwise generated in memory.
func (s *MockServer) startHTTP() error {
	mux := nethttp.NewServeMux()
	for i, ep := range s.cfg.Endpoints {
		method := ep.Method
		if method == "" {
			method = "POST"
		}
		h := s.handlers[i]
		mux.HandleFunc(ep.Path, makeHTTPHandler(method, h))
	}

	addr := stripScheme(s.cfg.Transport.URL)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http bind %s: %w", addr, err)
	}
	s.addr = ln.Addr().String()

	s.httpSrv = &nethttp.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	wantTLS := s.cfg.Transport.TLS != nil && s.cfg.Transport.TLS.Enabled
	if wantTLS {
		tlsCfg, certSource, err := s.buildServerTLSConfig()
		if err != nil {
			ln.Close()
			return err
		}
		s.httpSrv.TLSConfig = tlsCfg
		logTLSStartup(s.addr, certSource)
		tlsLn := tls.NewListener(ln, tlsCfg)
		go func() { _ = s.httpSrv.Serve(tlsLn) }()
		return nil
	}

	logHTTPStartup(s.addr)
	go func() {
		// Serve returns ErrServerClosed on a clean shutdown — we don't
		// log it. Other errors are silent in v1; users notice them via
		// connection failures in the load generator.
		_ = s.httpSrv.Serve(ln)
	}()
	return nil
}

// buildServerTLSConfig assembles the tls.Config for a TLS-enabled mock
// listener. When cert_file+key_file are set it loads them; otherwise it
// generates a self-signed cert in memory. The second return value is a
// human-readable label for the startup log line.
func (s *MockServer) buildServerTLSConfig() (*tls.Config, string, error) {
	tcfg := s.cfg.Transport.TLS
	if tcfg.CertFile != "" {
		pair, err := LoadCertPair(tcfg.CertFile, tcfg.KeyFile)
		if err != nil {
			return nil, "", err
		}
		return &tls.Config{Certificates: []tls.Certificate{pair}}, "cert=" + tcfg.CertFile, nil
	}
	pair, err := GenerateSelfSignedCert()
	if err != nil {
		return nil, "", err
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}}, "auto-generated self-signed cert, valid 24h", nil
}

func logTLSStartup(addr, certSource string) {
	fmt.Fprintf(os.Stderr, "mock: listening on https://%s  (TLS, %s)\n", addr, certSource)
}

func logHTTPStartup(addr string) {
	fmt.Fprintf(os.Stderr, "mock: listening on http://%s\n", addr)
}

// Addr returns the bound listener address (host:port) once Start has
// completed. Empty before Start.
func (s *MockServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// makeHTTPHandler wraps one mock Handler in an http.HandlerFunc.
func makeHTTPHandler(method string, h *Handler) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method != method {
			w.WriteHeader(nethttp.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(nethttp.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		reply, noAnswer, err := h.Handle(body)
		if err != nil {
			w.WriteHeader(nethttp.StatusInternalServerError)
			return
		}
		if noAnswer {
			// Simulate a hung server: block until the client gives up
			// (its response_timeout cancels the request context). We
			// never call Write/WriteHeader, so the client sees a clean
			// "context deadline exceeded" instead of a 0-byte 200.
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}
}

// Stop shuts down whichever transport is active. Safe to call multiple times.
func (s *MockServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil
	}
	s.started = false

	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.subs = nil
	if s.natsConn != nil {
		s.natsConn.Close()
		s.natsConn = nil
	}
	if s.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := s.httpSrv.Shutdown(ctx)
		s.httpSrv = nil
		if err != nil {
			return err
		}
	}
	return nil
}

// Endpoints returns a short description of every configured endpoint —
// used by the `mock` command for the startup message.
func (s *MockServer) Endpoints() []string {
	out := make([]string, 0, len(s.cfg.Endpoints))
	for _, ep := range s.cfg.Endpoints {
		out = append(out, endpointLabel(ep))
	}
	return out
}

// stripScheme converts "http://host:port" or "https://host:port" into the
// plain "host:port" form net.Listen wants. Bare "host:port" inputs pass
// through unchanged.
func stripScheme(url string) string {
	if i := strings.Index(url, "://"); i >= 0 {
		return url[i+3:]
	}
	return url
}
