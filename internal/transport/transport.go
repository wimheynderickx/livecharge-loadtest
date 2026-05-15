package transport

import (
	"context"
	"time"
)

// Request is a protocol-agnostic outgoing message.
//
// The same struct serves both NATS and HTTP. Fields that are only meaningful
// for one transport are documented as such; transports ignore fields that
// don't apply to them.
type Request struct {
	// Subject is the NATS subject (NATS only).
	Subject string

	// Method is the HTTP method (HTTP only).
	Method string

	// Path is the HTTP path appended to the transport URL (HTTP only).
	Path string

	// Headers attaches metadata to the request. NATS uses these as
	// nats.Msg.Header; HTTP uses them as HTTP request headers.
	Headers map[string]string

	// Body is the serialised request payload.
	Body []byte

	// Timeout caps how long Send waits for a reply. Zero means "no timeout"
	// (use Send's context for cancellation in that case).
	Timeout time.Duration

	// FireAndForget asks the transport to publish without expecting a reply.
	// Only NATS honours this flag; HTTP always reads a response.
	FireAndForget bool
}

// Response is a protocol-agnostic reply.
type Response struct {
	// Body is the reply payload bytes.
	Body []byte

	// Headers are HTTP response headers (HTTP only).
	Headers map[string]string

	// StatusCode is the HTTP status code (HTTP only).
	StatusCode int

	// Meta are NATS reply headers, exposed to the extractor as meta/X.
	Meta map[string]string

	// Latency is the round-trip duration measured by the transport.
	Latency time.Duration
}

// Transport is the contract every protocol implementation must fulfil.
//
// A single Transport instance is shared across all concurrent sessions of
// one scenario; implementations must therefore be safe for concurrent Send
// calls. Close is called once when the scenario stops for good.
type Transport interface {
	Send(ctx context.Context, req Request) (Response, error)
	Close() error

	// Protocol returns a short human label for the wire protocol the
	// transport is actually using. Safe to call from any goroutine.
	// Returns "(intent)" or "(negotiating)" suffixed labels before the
	// first response settles the answer; after that it returns the
	// observed value (e.g. "HTTP/2 (h2)", "HTTP/1.1", "NATS 2.10").
	Protocol() string
}
