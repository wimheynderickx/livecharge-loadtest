package config

// MockConfig is the top-level structure for a mock-server TOML file.
// A mock server hosts one or more endpoints, each replying to a NATS subject
// or HTTP path with a templated OK or FAIL response.
type MockConfig struct {
	// Transport defines how the mock server listens for requests. The same
	// TransportConfig type is used as for scenarios; here, URL is the
	// listening address (e.g. "nats://localhost:4222" or "localhost:8080").
	Transport TransportConfig `toml:"transport"`

	// Endpoints are the request handlers, one per [[endpoint]] block.
	Endpoints []MockEndpointConfig `toml:"endpoint"`
}

// MockEndpointConfig is one [[endpoint]] block: the address to listen on,
// optional field extractions from the incoming request, and the OK/FAIL
// reply templates.
type MockEndpointConfig struct {
	// Subject is the NATS subject to subscribe to (NATS transport only).
	Subject string `toml:"subject"`

	// Path is the HTTP request path to handle (HTTP transport only).
	Path string `toml:"path"`

	// Method is the HTTP method to handle (HTTP transport only).
	// Defaults to "POST" if empty.
	Method string `toml:"method"`

	// FailRate is the fraction of replies that use FailResponse instead of
	// OkResponse. Range: [0.0, 1.0]. 0 means "always OK", 1 means "always FAIL".
	FailRate float64 `toml:"fail_rate"`

	// NoAnswerRate is the fraction of incoming requests for which the mock
	// does not respond at all. Range: [0.0, 1.0]. The roll happens before
	// FailRate, so the OK/FAIL split applies only to the requests that
	// did get answered: a (no_answer=0.1, fail=0.1) configuration produces
	// 10% no-answer, 9% FAIL, and 81% OK.
	//
	// Semantics per transport:
	//   - NATS: the subscription handler returns without calling Respond.
	//           The loadtest client then times out per response_timeout.
	//   - HTTP: the handler blocks until the client closes the connection
	//           (i.e. its response_timeout elapses), so the client side
	//           sees a "context deadline exceeded" error.
	NoAnswerRate float64 `toml:"no_answer_rate"`

	// Extracts pulls fields from the incoming request body into .extracted
	// for use in the response templates. Only JSON-body extraction is
	// supported here — no headers, no status, no meta.
	Extracts []ExtractConfig `toml:"extract"`

	// OkResponse is a template string for the success reply.
	// Namespace: .extracted.X.
	OkResponse string `toml:"ok_response"`

	// FailResponse is a template string for the failure reply.
	// Namespace: .extracted.X.
	FailResponse string `toml:"fail_response"`
}
