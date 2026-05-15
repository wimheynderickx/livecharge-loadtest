// Package http implements transport.Transport on top of net/http.
//
// One Transport instance owns one *http.Client. The Go HTTP client is safe
// for concurrent use and pools connections internally, so all sessions of a
// scenario share the same Transport — re-creating the client per session
// would defeat connection reuse and cripple throughput.
package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/transport"
)

// Transport sends requests over HTTP or HTTPS.
type Transport struct {
	client  *nethttp.Client
	baseURL string
	auth    config.AuthConfig

	// preBuiltAuth caches the value of the Authorization header so we don't
	// rebuild the same Basic/Bearer string on every request.
	preBuiltAuth string

	proto *protocolTracker // tracks observed wire protocol
}

// New constructs a Transport with a shared http.Client tuned for sustained
// load: many idle conns per host, short keep-alive timeout to avoid stale
// sockets. The URL scheme selects the wire protocol:
//
//   - http://  → HTTP/1.1 over plain TCP
//   - h2c://   → HTTP/2 prior-knowledge over plain TCP
//   - https:// → ALPN-negotiated TLS (h2 preferred, h1.1 fallback)
func New(cfg config.TransportConfig) (*Transport, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("http transport: parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)

	tlsCfg, err := buildTLSConfig(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("http transport: tls: %w", err)
	}

	wantH2 := cfg.HTTP2Opt == nil || *cfg.HTTP2Opt // default true

	t := &Transport{
		baseURL: strings.TrimRight(cfg.URL, "/"),
		auth:    cfg.Auth,
	}

	switch scheme {
	case "http":
		t.client = &nethttp.Client{
			Transport: &nethttp.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     30 * time.Second,
			},
			Timeout: 0,
		}
		t.proto = newProtocolTracker("HTTP/1.1 (intent)")

	case "h2c":
		h2tr := &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		}
		// Rewrite baseURL so step paths append to http:// (not h2c://).
		t.baseURL = "http://" + parsed.Host + strings.TrimRight(parsed.Path, "/")
		t.client = &nethttp.Client{Transport: h2tr, Timeout: 0}
		t.proto = newProtocolTracker("HTTP/2 (h2c, intent)")

	case "https":
		if wantH2 {
			std := &nethttp.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     30 * time.Second,
				TLSClientConfig:     tlsCfg,
				ForceAttemptHTTP2:   true,
			}
			t.client = &nethttp.Client{Transport: std, Timeout: 0}
			t.proto = newProtocolTracker("HTTPS (h2 preferred, negotiating)")
		} else {
			std := &nethttp.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     30 * time.Second,
				TLSClientConfig:     tlsCfg,
				TLSNextProto:        map[string]func(string, *tls.Conn) nethttp.RoundTripper{},
			}
			t.client = &nethttp.Client{Transport: std, Timeout: 0}
			t.proto = newProtocolTracker("HTTPS (h1.1 forced)")
		}

	default:
		return nil, fmt.Errorf("http transport: unsupported URL scheme %q (want http, h2c, or https)", scheme)
	}

	if cfg.TLS != nil && cfg.TLS.InsecureSkipVerify {
		fmt.Fprintf(os.Stderr, "WARN: TLS verification disabled for scenario via [transport.tls].insecure_skip_verify=true\n")
	}

	switch cfg.Auth.Type {
	case "", "none":
		// no header
	case "basic":
		creds := cfg.Auth.Username + ":" + cfg.Auth.Password
		t.preBuiltAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
	case "jwt":
		t.preBuiltAuth = "Bearer " + cfg.Auth.Token
	default:
		return nil, fmt.Errorf("http transport: unsupported auth type %q", cfg.Auth.Type)
	}

	return t, nil
}

// Send issues one HTTP request and returns the parsed response.
//
// Latency is measured around client.Do; we don't include body-reading time
// in the per-step latency because a slow client (i.e. us reading the body)
// would inflate the server-side measurement we actually care about.
func (t *Transport) Send(ctx context.Context, req transport.Request) (transport.Response, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	url := t.baseURL + req.Path
	httpReq, err := nethttp.NewRequestWithContext(ctx, req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		return transport.Response{}, fmt.Errorf("http build request: %w", err)
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	// Caller-supplied Authorization wins over auto-generated.
	if t.preBuiltAuth != "" && httpReq.Header.Get("Authorization") == "" {
		httpReq.Header.Set("Authorization", t.preBuiltAuth)
	}
	if httpReq.Header.Get("Content-Type") == "" && len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	httpResp, err := t.client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		return transport.Response{Latency: latency}, fmt.Errorf("http send: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.Proto != "" {
		t.proto.Settle(httpResp.Proto)
	}

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return transport.Response{Latency: latency, StatusCode: httpResp.StatusCode},
			fmt.Errorf("http read body: %w", err)
	}

	headers := make(map[string]string, len(httpResp.Header))
	for k, vs := range httpResp.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	return transport.Response{
		Body:       body,
		Headers:    headers,
		StatusCode: httpResp.StatusCode,
		Latency:    latency,
	}, nil
}

// Close shuts down the underlying connection pool. The Go HTTP client has
// no explicit close, so we just signal idle connections to close.
func (t *Transport) Close() error {
	if tr, ok := t.client.Transport.(*nethttp.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}

// Protocol returns the current wire-protocol label. Before the first
// response this is the intent string; after the first response it reflects
// what the server actually negotiated.
func (t *Transport) Protocol() string {
	return t.proto.Get()
}
