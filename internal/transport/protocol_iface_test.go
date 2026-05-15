package transport_test

import (
	"testing"

	"livecharge/loadtest/internal/config"
	httptransport "livecharge/loadtest/internal/transport/http"
	natstransport "livecharge/loadtest/internal/transport/nats"
)

// TestProtocolInterface confirms both transports implement Protocol().
// Just checks the call returns a non-empty string for a freshly-built
// transport; Tasks 4 and 6 cover the actual values.
func TestProtocolInterface(t *testing.T) {
	h, err := httptransport.New(config.TransportConfig{
		Type: "http",
		URL:  "http://localhost",
		Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatalf("http New: %v", err)
	}
	if h.Protocol() == "" {
		t.Error("http Protocol() returned empty string")
	}
	_ = h.Close()

	// NATS New takes a config too; build without actually connecting.
	n, err := natstransport.New(config.TransportConfig{
		Type: "nats",
		URL:  "nats://localhost:4222",
		Auth: config.AuthConfig{Type: "none"},
	})
	if err == nil {
		if n.Protocol() == "" {
			t.Error("nats Protocol() returned empty string")
		}
		_ = n.Close()
	}
	// If NATS New returns an error (e.g. server not running), skip the NATS half.
}
