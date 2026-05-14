package engine

import (
	"fmt"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/transport"
	httptransport "livecharge/loadtest/internal/transport/http"
	natstransport "livecharge/loadtest/internal/transport/nats"
)

// newTransport picks the right transport implementation for cfg.
//
// We dispatch here (rather than inside the transport package) to avoid a
// circular import: the sub-packages need transport.Request and
// transport.Response from the parent.
func newTransport(cfg config.TransportConfig) (transport.Transport, error) {
	switch cfg.Type {
	case "nats":
		return natstransport.New(cfg)
	case "http", "https":
		return httptransport.New(cfg)
	default:
		return nil, fmt.Errorf("unknown transport type %q", cfg.Type)
	}
}
