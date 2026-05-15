package http

import "testing"

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
