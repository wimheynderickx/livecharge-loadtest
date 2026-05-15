package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Defaults that are also referenced from load_files.go.
const (
	defaultResponseTimeout = 2 * time.Second
	defaultFlushInterval   = 10 * time.Second
)

// ValidationError is one problem found in a config file.
type ValidationError struct {
	// Section is the dotted path to the offending field
	// (e.g. "load.response_timeout" or "step[0].predicate[1].op").
	Section string
	// Message describes the problem in plain English.
	Message string
}

func (e ValidationError) Error() string {
	if e.Section == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Section, e.Message)
}

// ValidationErrors collects every problem from a single file. Returning all
// of them at once (rather than just the first) lets users fix bad configs in
// one pass.
type ValidationErrors struct {
	File   string
	Errors []ValidationError
}

func (e *ValidationErrors) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d problem(s) in %s:\n", len(e.Errors), e.File)
	for _, ve := range e.Errors {
		fmt.Fprintf(&b, "  - %s\n", ve.Error())
	}
	return b.String()
}

// ValidateScenario checks a parsed scenario against the cross-field rules
// described in the design spec. It returns every error it finds.
func ValidateScenario(cfg *ScenarioConfig, md toml.MetaData) []ValidationError {
	var errs []ValidationError

	// --- [scenario] block --------------------------------------------------
	if cfg.Scenario.Name == "" {
		errs = append(errs, ValidationError{"scenario.name", "must not be empty"})
	}

	// --- [transport] block -------------------------------------------------
	switch cfg.Transport.Type {
	case "nats", "http", "https":
		// ok
	case "":
		errs = append(errs, ValidationError{"transport.type", "must be set (nats, http, or https)"})
	default:
		errs = append(errs, ValidationError{
			"transport.type",
			fmt.Sprintf("unknown transport %q (valid: nats, http, https)", cfg.Transport.Type),
		})
	}
	if cfg.Transport.URL == "" {
		errs = append(errs, ValidationError{"transport.url", "must not be empty"})
	} else {
		parsed, perr := url.Parse(cfg.Transport.URL)
		if perr != nil {
			errs = append(errs, ValidationError{"transport.url", "invalid URL: " + perr.Error()})
		} else {
			scheme := strings.ToLower(parsed.Scheme)

			if scheme == "h2c" && cfg.Transport.HTTP2Opt != nil && !*cfg.Transport.HTTP2Opt {
				errs = append(errs, ValidationError{
					"transport.http2",
					"http2=false has no effect on h2c:// URLs; remove the scheme or the flag",
				})
			}
			if cfg.Transport.TLS != nil && scheme != "https" {
				errs = append(errs, ValidationError{
					"transport.tls",
					"[transport.tls] requires https:// scheme; got " + parsed.Scheme,
				})
			}
			if cfg.Transport.TLS != nil && cfg.Transport.TLS.CAFile != "" {
				if _, err := os.Stat(cfg.Transport.TLS.CAFile); err != nil {
					errs = append(errs, ValidationError{
						"transport.tls.ca_file",
						err.Error(),
					})
				}
			}
		}
	}

	// --- [transport.auth] block --------------------------------------------
	errs = append(errs, validateAuth(cfg.Transport.Type, cfg.Transport.Auth)...)

	// --- [load] block ------------------------------------------------------
	if cfg.Load.Concurrency < 1 {
		errs = append(errs, ValidationError{
			"load.concurrency",
			fmt.Sprintf("must be >= 1 (got %d)", cfg.Load.Concurrency),
		})
	}
	if cfg.Load.ResponseTimeout.Duration <= 0 {
		errs = append(errs, ValidationError{"load.response_timeout", "must be > 0"})
	}
	if cfg.Load.Rate < 0 {
		errs = append(errs, ValidationError{"load.rate", "must be >= 0"})
	}
	if cfg.Load.TotalMessages < 0 {
		errs = append(errs, ValidationError{"load.total_messages", "must be >= 0"})
	}
	if cfg.Load.Duration.Duration < 0 {
		errs = append(errs, ValidationError{"load.duration", "must be >= 0"})
	}

	// --- [context] block ---------------------------------------------------
	errs = append(errs, validateContext(cfg.Context, md)...)

	// --- [[step]] blocks ---------------------------------------------------
	errs = append(errs, validateSteps(cfg)...)

	// --- [metrics] block ---------------------------------------------------
	for i, p := range cfg.Metrics.Percentiles {
		if p < 0 || p > 100 {
			errs = append(errs, ValidationError{
				fmt.Sprintf("metrics.percentiles[%d]", i),
				fmt.Sprintf("must be in [0, 100] (got %g)", p),
			})
		}
	}
	errs = append(errs, validateBuckets(cfg.Metrics)...)

	// --- [report] block ----------------------------------------------------
	if cfg.Report != nil {
		if cfg.Report.CSVPath == "" {
			errs = append(errs, ValidationError{"report.csv_path", "must not be empty when [report] is present"})
		}
		if cfg.Report.FlushInterval.Duration <= 0 {
			errs = append(errs, ValidationError{"report.flush_interval", "must be > 0"})
		}
	}

	return errs
}

// validateAuth checks the auth block in isolation from transport rules,
// then enforces compatibility between transport.type and auth.type.
func validateAuth(transport string, auth AuthConfig) []ValidationError {
	var errs []ValidationError

	switch auth.Type {
	case "", "none":
		// always ok
	case "userpass":
		if transport != "nats" {
			errs = append(errs, ValidationError{
				"transport.auth.type",
				`"userpass" is only valid for transport type "nats"`,
			})
		}
		if auth.Username == "" || auth.Password == "" {
			errs = append(errs, ValidationError{
				"transport.auth",
				`"userpass" requires both username and password`,
			})
		}
	case "basic":
		if transport != "http" && transport != "https" {
			errs = append(errs, ValidationError{
				"transport.auth.type",
				`"basic" is only valid for transport type "http" or "https"`,
			})
		}
		if auth.Username == "" || auth.Password == "" {
			errs = append(errs, ValidationError{
				"transport.auth",
				`"basic" requires both username and password`,
			})
		}
	case "jwt":
		if transport != "http" && transport != "https" {
			errs = append(errs, ValidationError{
				"transport.auth.type",
				`"jwt" is only valid for transport type "http" or "https"`,
			})
		}
		if auth.Token == "" {
			errs = append(errs, ValidationError{"transport.auth.token", `"jwt" requires a non-empty token`})
		}
	default:
		errs = append(errs, ValidationError{
			"transport.auth.type",
			fmt.Sprintf("unknown auth type %q (valid: none, userpass, basic, jwt)", auth.Type),
		})
	}

	return errs
}

// validateContext does a light sanity check on each [context] entry without
// fully decoding the generators. Full decoding happens in the template
// package, which has access to the toml.MetaData.
func validateContext(ctx map[string]toml.Primitive, md toml.MetaData) []ValidationError {
	var errs []ValidationError
	for key, prim := range ctx {
		// Try to decode as a generator description; if it succeeds and Type
		// is set, validate the inner fields. If decode fails or Type is
		// empty, assume the entry is a static scalar (validated implicitly
		// when the template package reads it).
		var def ContextValueConfig
		if err := md.PrimitiveDecode(prim, &def); err != nil {
			// Probably a scalar — accept silently.
			continue
		}
		if def.Type == "" {
			continue
		}

		section := "context." + key
		switch def.Type {
		case "sequence":
			if def.Step < 1 {
				if def.Step == 0 {
					// step defaults to 1 at runtime, that's fine
				} else {
					errs = append(errs, ValidationError{
						section + ".step",
						fmt.Sprintf("must be >= 1 (got %d)", def.Step),
					})
				}
			}
		case "random_range":
			if def.Min >= def.Max {
				errs = append(errs, ValidationError{
					section,
					fmt.Sprintf("random_range requires min < max (got min=%d, max=%d)", def.Min, def.Max),
				})
			}
		case "random_pick":
			if len(def.Values) == 0 {
				errs = append(errs, ValidationError{
					section + ".values",
					"random_pick requires at least one value",
				})
			}
		default:
			errs = append(errs, ValidationError{
				section + ".type",
				fmt.Sprintf("unknown generator %q (valid: sequence, random_range, random_pick)", def.Type),
			})
		}
	}
	return errs
}

// validateSteps walks the [[step]] list and checks per-step rules plus
// cross-step rules (unique names, predicate next_step references).
func validateSteps(cfg *ScenarioConfig) []ValidationError {
	var errs []ValidationError

	if len(cfg.Steps) == 0 {
		errs = append(errs, ValidationError{"step", "scenario must define at least one [[step]]"})
		return errs
	}

	// Build the set of step names for predicate next_step lookups.
	names := make(map[string]bool, len(cfg.Steps))
	for i, s := range cfg.Steps {
		section := fmt.Sprintf("step[%d]", i)
		if s.Name == "" {
			errs = append(errs, ValidationError{section + ".name", "must not be empty"})
		} else if names[s.Name] {
			errs = append(errs, ValidationError{
				section + ".name",
				fmt.Sprintf("duplicate step name %q", s.Name),
			})
		} else {
			names[s.Name] = true
		}

		if s.Template == "" {
			errs = append(errs, ValidationError{section + ".template", "must not be empty"})
		}

		// Transport-specific fields.
		switch cfg.Transport.Type {
		case "nats":
			if s.Subject == "" {
				errs = append(errs, ValidationError{section + ".subject", "NATS step must set subject"})
			}
		case "http", "https":
			if s.Path == "" {
				errs = append(errs, ValidationError{section + ".path", "HTTP step must set path"})
			}
			if s.Method == "" {
				errs = append(errs, ValidationError{section + ".method", "HTTP step must set method"})
			}
		}

		if s.FireAndForget && cfg.Transport.Type != "nats" {
			errs = append(errs, ValidationError{
				section + ".fire_and_forget",
				"fire_and_forget is only valid for NATS transport",
			})
		}

		if s.ResponseTimeout != nil && s.ResponseTimeout.Duration <= 0 {
			errs = append(errs, ValidationError{
				section + ".response_timeout",
				"must be > 0 when set",
			})
		}

		// Validate predicates that belong to this step.
		for j, p := range s.Predicates {
			psection := fmt.Sprintf("%s.predicate[%d]", section, j)
			switch p.Op {
			case "eq", "ne", "contains", "gt", "lt":
				if p.Field == "" {
					errs = append(errs, ValidationError{psection + ".field", "must not be empty"})
				}
			case "expr":
				if strings.TrimSpace(p.Value) == "" {
					errs = append(errs, ValidationError{psection + ".value", "value must not be empty when op=expr"})
				}
				// field is silently ignored with op=expr; surfaced via ValidateWarnings.
			case "":
				errs = append(errs, ValidationError{psection + ".op", "must be set"})
			default:
				errs = append(errs, ValidationError{
					psection + ".op",
					fmt.Sprintf("unknown op %q (valid: eq, ne, contains, gt, lt, expr)", p.Op),
				})
			}
			if p.Name == "" {
				errs = append(errs, ValidationError{psection + ".name", "must not be empty"})
			}
		}

		// Validate extracts.
		for j, e := range s.Extracts {
			esection := fmt.Sprintf("%s.extract[%d]", section, j)
			if e.Field == "" {
				errs = append(errs, ValidationError{esection + ".field", "must not be empty"})
			}
			if e.Path == "" {
				errs = append(errs, ValidationError{esection + ".path", "must not be empty"})
			}
		}
	}

	// Second pass: every predicate next_step must reference a known step
	// or be the empty string.
	for i, s := range cfg.Steps {
		for j, p := range s.Predicates {
			if p.NextStep == "" {
				continue
			}
			if !names[p.NextStep] {
				errs = append(errs, ValidationError{
					fmt.Sprintf("step[%d].predicate[%d].next_step", i, j),
					fmt.Sprintf("references unknown step %q", p.NextStep),
				})
			}
		}
	}

	return errs
}

// validateBuckets checks the optional latency-bucket configuration.
//
// Rules:
//   - bucket_count and bucket_edges_ms are mutually exclusive
//   - bucket_count, when set, must be >= 2 (otherwise the chart is a single
//     bar and there's no point)
//   - bucket_edges_ms entries must be strictly increasing and all > 0
func validateBuckets(m MetricsConfig) []ValidationError {
	var errs []ValidationError

	if m.BucketCount != 0 && len(m.BucketEdgesMs) > 0 {
		errs = append(errs, ValidationError{
			"metrics",
			"bucket_count and bucket_edges_ms are mutually exclusive; set only one",
		})
	}

	if m.BucketCount != 0 && m.BucketCount < 2 {
		errs = append(errs, ValidationError{
			"metrics.bucket_count",
			fmt.Sprintf("must be >= 2 (got %d)", m.BucketCount),
		})
	}

	var prev float64
	for i, e := range m.BucketEdgesMs {
		section := fmt.Sprintf("metrics.bucket_edges_ms[%d]", i)
		if e <= 0 {
			errs = append(errs, ValidationError{section, fmt.Sprintf("must be > 0 (got %g)", e)})
		}
		if i > 0 && e <= prev {
			errs = append(errs, ValidationError{
				section,
				fmt.Sprintf("must be strictly greater than the previous edge (got %g after %g)", e, prev),
			})
		}
		prev = e
	}

	return errs
}

// Validate is a convenience wrapper around ValidateScenario for callers that
// do not have a toml.MetaData (e.g. tests constructing configs programmatically).
// It passes a zero MetaData, which is safe when cfg.Context is nil or empty.
func Validate(cfg *ScenarioConfig) []ValidationError {
	return ValidateScenario(cfg, toml.MetaData{})
}

// ValidationWarning is a non-fatal config issue. Surfaced by ValidateWarnings.
// Validate() does not return these — they don't prevent the scenario from
// starting, they just inform the user.
type ValidationWarning struct {
	Field   string
	Message string
}

// ValidateWarnings collects warnings for a scenario config.
//
// Currently it covers: op=expr with field set (field is unused).
// Add new warning types here as the language grows.
func ValidateWarnings(cfg *ScenarioConfig) []ValidationWarning {
	var warns []ValidationWarning
	for i, s := range cfg.Steps {
		section := fmt.Sprintf("step[%d]", i)
		for j, p := range s.Predicates {
			psection := fmt.Sprintf("%s.predicate[%d]", section, j)
			if p.Op == "expr" && p.Field != "" {
				warns = append(warns, ValidationWarning{
					Field:   psection + ".field",
					Message: "field is unused with op=expr (ignored)",
				})
			}
		}
	}
	return warns
}

// ValidateMock checks a mock-server config.
func ValidateMock(cfg *MockConfig) []ValidationError {
	var errs []ValidationError

	switch cfg.Transport.Type {
	case "nats", "http", "https":
		// ok
	case "":
		errs = append(errs, ValidationError{"transport.type", "must be set (nats, http, or https)"})
	default:
		errs = append(errs, ValidationError{
			"transport.type",
			fmt.Sprintf("unknown transport %q", cfg.Transport.Type),
		})
	}
	if cfg.Transport.URL == "" {
		errs = append(errs, ValidationError{"transport.url", "must not be empty"})
	}
	if len(cfg.Endpoints) == 0 {
		errs = append(errs, ValidationError{"endpoint", "mock must define at least one [[endpoint]]"})
	}

	// TLS cert/key pair.
	if cfg.Transport.TLS != nil {
		hasCert := cfg.Transport.TLS.CertFile != ""
		hasKey := cfg.Transport.TLS.KeyFile != ""
		if hasCert != hasKey {
			errs = append(errs, ValidationError{
				"transport.tls",
				"cert_file and key_file must both be set or both empty",
			})
		}
		if hasCert {
			if _, err := os.Stat(cfg.Transport.TLS.CertFile); err != nil {
				errs = append(errs, ValidationError{"transport.tls.cert_file", err.Error()})
			}
		}
		if hasKey {
			if _, err := os.Stat(cfg.Transport.TLS.KeyFile); err != nil {
				errs = append(errs, ValidationError{"transport.tls.key_file", err.Error()})
			}
		}
	}

	// HTTP/2 tunable bounds.
	if h := cfg.Transport.HTTP2; h != nil {
		const maxInt31 = 1<<31 - 1
		if h.MaxConcurrentStreams < 0 || h.MaxConcurrentStreams > maxInt31 {
			errs = append(errs, ValidationError{
				"transport.http2.max_concurrent_streams",
				"must be between 1 and 2^31-1",
			})
		}
		if h.InitialStreamWindowSize != 0 && (h.InitialStreamWindowSize < 1 || h.InitialStreamWindowSize > maxInt31) {
			errs = append(errs, ValidationError{
				"transport.http2.initial_stream_window_size",
				"must be between 1 and 2^31-1",
			})
		}
		if h.InitialConnWindowSize != 0 && h.InitialStreamWindowSize != 0 &&
			h.InitialConnWindowSize < h.InitialStreamWindowSize {
			errs = append(errs, ValidationError{
				"transport.http2.initial_conn_window_size",
				"initial_conn_window_size must be >= initial_stream_window_size",
			})
		}
		if h.MaxFrameSize != 0 && (h.MaxFrameSize < 16384 || h.MaxFrameSize > 16777215) {
			errs = append(errs, ValidationError{
				"transport.http2.max_frame_size",
				"max_frame_size must be between 16384 and 16777215",
			})
		}
	}

	for i, ep := range cfg.Endpoints {
		section := fmt.Sprintf("endpoint[%d]", i)
		switch cfg.Transport.Type {
		case "nats":
			if ep.Subject == "" {
				errs = append(errs, ValidationError{section + ".subject", "must be set for NATS mocks"})
			}
		case "http", "https":
			if ep.Path == "" {
				errs = append(errs, ValidationError{section + ".path", "must be set for HTTP mocks"})
			}
		}
		if ep.FailRate < 0 || ep.FailRate > 1 {
			errs = append(errs, ValidationError{
				section + ".fail_rate",
				fmt.Sprintf("must be in [0, 1] (got %g)", ep.FailRate),
			})
		}
		if ep.NoAnswerRate < 0 || ep.NoAnswerRate > 1 {
			errs = append(errs, ValidationError{
				section + ".no_answer_rate",
				fmt.Sprintf("must be in [0, 1] (got %g)", ep.NoAnswerRate),
			})
		}
		if ep.OkResponse == "" {
			errs = append(errs, ValidationError{section + ".ok_response", "must not be empty"})
		}
		if ep.FailResponse == "" && ep.FailRate > 0 {
			errs = append(errs, ValidationError{
				section + ".fail_response",
				"must be set when fail_rate > 0",
			})
		}
		if ep.Stream != nil {
			if ep.Stream.Chunks < 1 {
				errs = append(errs, ValidationError{
					section + ".stream.chunks", "chunks must be >= 1",
				})
			}
			if ep.Stream.DelayMs < 0 {
				errs = append(errs, ValidationError{
					section + ".stream.delay_ms", "delay_ms must be >= 0",
				})
			}
		}
	}

	return errs
}
