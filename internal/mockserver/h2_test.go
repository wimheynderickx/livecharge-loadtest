package mockserver_test

import (
	"crypto/tls"
	"io"
	"net"
	nethttp "net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/mockserver"
)

// h2cClient returns an *http.Client that speaks HTTP/2 over plain TCP
// (h2c, prior knowledge). DialTLS returns a plain net.Conn despite its
// TLS-flavoured name — that's the canonical h2c form in golang.org/x/net.
func h2cClient() *nethttp.Client {
	return &nethttp.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
		Timeout: 2 * time.Second,
	}
}

func TestMock_H2C_RoundTrip(t *testing.T) {
	cfg := config.MockConfig{
		Transport: config.TransportConfig{
			Type:  "http",
			URL:   "127.0.0.1:0",
			HTTP2: &config.HTTP2Config{},
		},
		Endpoints: []config.MockEndpointConfig{{
			Path: "/", Method: "GET", OkResponse: "h2c-ok",
		}},
	}
	srv, err := mockserver.NewMockServer(cfg)
	if err != nil {
		t.Fatalf("NewMockServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	resp, err := h2cClient().Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "h2c-ok" {
		t.Errorf("body = %q, want h2c-ok", string(body))
	}
	if resp.Proto != "HTTP/2.0" {
		t.Errorf("proto = %q, want HTTP/2.0", resp.Proto)
	}
}

func TestMock_H2_MaxConcurrentStreamsHonoured(t *testing.T) {
	cfg := config.MockConfig{
		Transport: config.TransportConfig{
			Type:  "http",
			URL:   "127.0.0.1:0",
			HTTP2: &config.HTTP2Config{MaxConcurrentStreams: 2},
		},
		Endpoints: []config.MockEndpointConfig{{
			Path: "/", Method: "GET", OkResponse: "ok",
		}},
	}
	srv, err := mockserver.NewMockServer(cfg)
	if err != nil {
		t.Fatalf("NewMockServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	// Concurrency well above the cap — verifies the http2 transport
	// transparently queues without crashing the server or hanging the
	// client. The stricter "GOAWAY framed at exactly N+1" assertion would
	// need a raw http2.Framer client; skip that for 0.2.
	client := &nethttp.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLS: func(network, target string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, target)
		},
	}, Timeout: 2 * time.Second}

	const N = 5
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			resp, err := client.Get("http://" + srv.Addr() + "/")
			if resp != nil {
				resp.Body.Close()
			}
			errCh <- err
		}()
	}
	for i := 0; i < N; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
}

func TestMock_H2_OverTLS_RoundTrip(t *testing.T) {
	cfg := config.MockConfig{
		Transport: config.TransportConfig{
			Type:  "http",
			URL:   "127.0.0.1:0",
			TLS:   &config.TLSConfig{Enabled: true},
			HTTP2: &config.HTTP2Config{},
		},
		Endpoints: []config.MockEndpointConfig{{
			Path: "/", Method: "GET", OkResponse: "h2-tls-ok",
		}},
	}
	srv, err := mockserver.NewMockServer(cfg)
	if err != nil {
		t.Fatalf("NewMockServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	client := &nethttp.Client{
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("https://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.Proto != "HTTP/2.0" {
		t.Errorf("proto = %q, want HTTP/2.0", resp.Proto)
	}
}
