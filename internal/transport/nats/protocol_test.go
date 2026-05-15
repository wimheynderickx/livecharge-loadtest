package nats

import (
	"testing"
)

func TestProtocol_FallbackWhenNoConn(t *testing.T) {
	tr := &Transport{} // unconnected
	tr.captureVersion()
	if got := tr.Protocol(); got != "NATS (connecting)" {
		t.Errorf("Protocol() with no conn = %q, want %q", got, "NATS (connecting)")
	}
}
