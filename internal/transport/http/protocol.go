package http

import (
	"strings"
	"sync/atomic"
)

// protocolTracker holds the human-facing protocol label.
//
// Before the first response, it carries the "intent" string set at
// transport construction. After the first response, Settle() rewrites
// it based on the observed *http.Response.Proto. Subsequent settles
// are allowed (a pool may dial a new connection that negotiates
// differently); each one overwrites the previous label.
//
// Atomic so the TUI can read every frame without contention.
type protocolTracker struct {
	v atomic.Value // string
}

func newProtocolTracker(intent string) *protocolTracker {
	pt := &protocolTracker{}
	pt.v.Store(intent)
	return pt
}

func (pt *protocolTracker) Get() string {
	if s, ok := pt.v.Load().(string); ok {
		return s
	}
	return ""
}

// Settle records the observed protocol given a *http.Response.Proto
// value (e.g. "HTTP/2.0", "HTTP/1.1"). The "intent" stored at
// construction guides how we phrase the settled label — specifically,
// if intent said "h2 preferred" but we settled to h1.1, the label
// shows "negotiated to h1.1" so the user can see the negotiation
// outcome without digging through logs.
func (pt *protocolTracker) Settle(observedProto string) {
	prev, _ := pt.v.Load().(string)
	label := settledLabel(observedProto, prev)
	pt.v.Store(label)
}

// settledLabel maps (observedProto, prevIntent) → user-facing label.
func settledLabel(observed, prev string) string {
	switch observed {
	case "HTTP/2.0", "HTTP/2":
		if strings.Contains(prev, "h2c") {
			return "HTTP/2 (h2c)"
		}
		return "HTTP/2 (h2)"
	case "HTTP/1.1", "HTTP/1.0":
		if strings.Contains(prev, "h2 preferred") {
			return "HTTP/1.1 (negotiated to h1.1)"
		}
		return "HTTP/1.1"
	}
	return observed
}
