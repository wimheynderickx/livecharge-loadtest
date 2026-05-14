package config

// StepConfig is one [[step]] block in a scenario file. A scenario runs the
// steps in order, with predicates allowing conditional jumps between them.
type StepConfig struct {
	// Name uniquely identifies the step within a scenario. It is also the
	// target of a predicate's NextStep field.
	Name string `toml:"name"`

	// Subject is the NATS subject when the transport is "nats".
	// For HTTP transports use Path instead.
	Subject string `toml:"subject"`

	// Method is the HTTP method ("GET", "POST", ...) when the transport
	// is "http"/"https". Ignored for NATS.
	Method string `toml:"method"`

	// Path is the HTTP request path (appended to the transport URL).
	// Ignored for NATS.
	Path string `toml:"path"`

	// Template is a Go text/template string that produces the JSON request
	// body. Available namespaces are .ctx (initialised per session) and
	// .session (extracted from previous step replies).
	Template string `toml:"template"`

	// FireAndForget is NATS-only. When true, the step publishes the message
	// without expecting a reply; no latency is recorded and no extracts
	// or predicates are evaluated.
	FireAndForget bool `toml:"fire_and_forget"`

	// ResponseTimeout overrides the scenario-level [load] response_timeout
	// for this single step. nil means "inherit". Use a step-level override
	// for one slow operation in an otherwise fast flow.
	ResponseTimeout *Duration `toml:"response_timeout"`

	// Headers attached to the outgoing request.
	// Both NATS (nats.Msg.Header) and HTTP support headers.
	// Each header value may be a template string.
	Headers []HeaderConfig `toml:"header"`

	// Extracts pull values from the reply and store them in .session for
	// use by subsequent steps. Evaluated in order; later entries can
	// reference earlier ones.
	Extracts []ExtractConfig `toml:"extract"`

	// Predicates control branching. They are evaluated in order after
	// all extracts have run; the first match wins.
	Predicates []PredicateConfig `toml:"predicate"`
}

// HeaderConfig is one [[step.header]] entry.
type HeaderConfig struct {
	// Name is the header key (e.g. "X-Correlation-Id", "Content-Type").
	Name string `toml:"name"`

	// Value is a template string. Same namespaces as StepConfig.Template.
	Value string `toml:"value"`
}

// ExtractConfig is one [[step.extract]] entry. It pulls a single value out
// of the response and stores it in the session's .session map under Field.
type ExtractConfig struct {
	// Field is the key under which the extracted value is stored.
	// Subsequent templates and predicates reference it as ".session.<Field>".
	Field string `toml:"field"`

	// Path is a slash-separated lookup path. The first segment selects the
	// source:
	//   "response/X/Y" — JSON body field path (NATS or HTTP)
	//   "body/X/Y"     — same as response, explicit prefix
	//   "header/X"     — HTTP response header
	//   "status"       — HTTP status code (as string)
	//   "meta/X"       — NATS reply header
	Path string `toml:"path"`
}

// PredicateConfig is one [[step.predicate]] entry. After a step's response
// has been extracted, predicates decide what to do next.
type PredicateConfig struct {
	// Name is the accounting key. Matched predicates increment the counter
	// stored under this name and record the step's latency into a per-name
	// histogram. Useful for distinguishing happy-flow vs error-flow latency.
	Name string `toml:"name"`

	// Field references either a session value ("session.X") or a top-level
	// response field ("status" for HTTP status code).
	Field string `toml:"field"`

	// Op is the comparison operator: eq | ne | contains | gt | lt.
	Op string `toml:"op"`

	// Value is the right-hand side of the comparison.
	Value string `toml:"value"`

	// NextStep is the name of the step to execute next when this predicate
	// matches. The empty string "" means "end the session now".
	NextStep string `toml:"next_step"`
}
