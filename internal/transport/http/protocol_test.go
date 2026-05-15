package http

import (
	"testing"

	"livecharge/loadtest/internal/config"
)

func TestIntent_PlainHTTP(t *testing.T) {
	tr := mustNew(t, "http://localhost:8080", nil, nil)
	if got := tr.Protocol(); got != "HTTP/1.1 (intent)" {
		t.Errorf("intent for http:// = %q", got)
	}
}

func TestIntent_H2C(t *testing.T) {
	tr := mustNew(t, "h2c://localhost:8080", nil, nil)
	if got := tr.Protocol(); got != "HTTP/2 (h2c, intent)" {
		t.Errorf("intent for h2c:// = %q", got)
	}
}

func TestIntent_HTTPS_DefaultH2(t *testing.T) {
	tr := mustNew(t, "https://api.example.com", nil, nil)
	if got := tr.Protocol(); got != "HTTPS (h2 preferred, negotiating)" {
		t.Errorf("intent for https:// = %q", got)
	}
}

func TestIntent_HTTPS_HTTP2False(t *testing.T) {
	f := false
	tr := mustNew(t, "https://api.example.com", &f, nil)
	if got := tr.Protocol(); got != "HTTPS (h1.1 forced)" {
		t.Errorf("intent for https:// + http2=false = %q", got)
	}
}

func mustNew(t *testing.T, url string, http2 *bool, tls *config.TLSConfig) *Transport {
	t.Helper()
	tr, err := New(config.TransportConfig{
		Type:  "http",
		URL:   url,
		Auth:  config.AuthConfig{Type: "none"},
		HTTP2: http2,
		TLS:   tls,
	})
	if err != nil {
		t.Fatalf("New(%s): %v", url, err)
	}
	return tr
}

func TestProtocolTracker_Intent(t *testing.T) {
	pt := newProtocolTracker("HTTP/1.1 (intent)")
	if got := pt.Get(); got != "HTTP/1.1 (intent)" {
		t.Errorf("Get() = %q, want %q", got, "HTTP/1.1 (intent)")
	}
}

func TestProtocolTracker_Settled(t *testing.T) {
	pt := newProtocolTracker("HTTP/2 (h2, negotiating)")
	pt.Settle("HTTP/2.0")
	if got := pt.Get(); got != "HTTP/2 (h2)" {
		t.Errorf("Get() = %q, want %q", got, "HTTP/2 (h2)")
	}
}

func TestProtocolTracker_NegotiatedToH1(t *testing.T) {
	// User asked for h2 over TLS but the server only offered h1.1.
	pt := newProtocolTracker("HTTPS (h2 preferred, negotiating)")
	pt.Settle("HTTP/1.1")
	if got := pt.Get(); got != "HTTP/1.1 (negotiated to h1.1)" {
		t.Errorf("Get() = %q, want %q", got, "HTTP/1.1 (negotiated to h1.1)")
	}
}

func TestProtocolTracker_Idempotent(t *testing.T) {
	// Settling twice with the same value is a no-op. Settling with a
	// different value updates (protocol can change on a new connection).
	pt := newProtocolTracker("HTTPS (h2 preferred, negotiating)")
	pt.Settle("HTTP/2.0")
	pt.Settle("HTTP/2.0")
	if got := pt.Get(); got != "HTTP/2 (h2)" {
		t.Errorf("after double-settle Get() = %q", got)
	}
}
