package http_test

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"livecharge/loadtest/internal/config"
	httptransport "livecharge/loadtest/internal/transport/http"
	"livecharge/loadtest/internal/transport"
)

func TestE2E_PlainHTTP(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tr := mustBuildTransport(t, srv.URL, nil, nil)
	defer tr.Close()

	_, err := tr.Send(context.Background(), transport.Request{Method: "GET", Path: "/", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := tr.Protocol(); got != "HTTP/1.1" {
		t.Errorf("protocol = %q, want HTTP/1.1", got)
	}
}

func TestE2E_H2C(t *testing.T) {
	h := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Write([]byte(`{"ok":true}`))
	})
	h2s := &http2.Server{}
	srv := httptest.NewUnstartedServer(h2c.NewHandler(h, h2s))
	srv.Start()
	defer srv.Close()

	h2cURL := "h2c://" + srv.Listener.Addr().String()

	tr := mustBuildTransport(t, h2cURL, nil, nil)
	defer tr.Close()

	_, err := tr.Send(context.Background(), transport.Request{Method: "GET", Path: "/", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := tr.Protocol(); got != "HTTP/2 (h2c)" {
		t.Errorf("protocol = %q, want HTTP/2 (h2c)", got)
	}
}

func TestE2E_HTTPS_NegotiatesH2(t *testing.T) {
	srv := httptest.NewUnstartedServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tlsCfg := &config.TLSConfig{InsecureSkipVerify: true}
	tr := mustBuildTransport(t, srv.URL, nil, tlsCfg)
	defer tr.Close()

	_, err := tr.Send(context.Background(), transport.Request{Method: "GET", Path: "/", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := tr.Protocol(); got != "HTTP/2 (h2)" {
		t.Errorf("protocol = %q, want HTTP/2 (h2)", got)
	}
}

func TestE2E_HTTPS_HTTP2False_NegotiatesH1(t *testing.T) {
	srv := httptest.NewUnstartedServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	f := false
	tlsCfg := &config.TLSConfig{InsecureSkipVerify: true}
	tr := mustBuildTransport(t, srv.URL, &f, tlsCfg)
	defer tr.Close()

	_, err := tr.Send(context.Background(), transport.Request{Method: "GET", Path: "/", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := tr.Protocol(); got != "HTTP/1.1" {
		t.Errorf("protocol = %q, want HTTP/1.1 (http2=false)", got)
	}
}

func mustBuildTransport(t *testing.T, rawURL string, http2Flag *bool, tlsCfg *config.TLSConfig) *httptransport.Transport {
	t.Helper()
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: rawURL, Auth: config.AuthConfig{Type: "none"},
		HTTP2: http2Flag, TLS: tlsCfg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}
