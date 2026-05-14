package template

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"livecharge/loadtest/internal/transport"
)

// Extract reads one value out of a transport.Response according to a
// slash-separated path. The first segment selects the source:
//
//	response/...  →  parse Body as JSON, descend keys
//	body/...      →  same as response (alias)
//	header/Name   →  look up Headers[Name]
//	status        →  StatusCode as string (HTTP only)
//	meta/Name     →  look up Meta[Name]   (NATS reply headers)
//
// The function always returns a string. Numeric predicates parse the value
// at compare time; this avoids hard-coding a type system here.
func Extract(resp transport.Response, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty extract path")
	}

	// Special-case: a bare "status" path with no slash.
	if path == "status" {
		return strconv.Itoa(resp.StatusCode), nil
	}

	parts := strings.SplitN(path, "/", 2)
	prefix := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch prefix {
	case "response", "body":
		return extractJSON(resp.Body, rest)
	case "header":
		if v, ok := resp.Headers[rest]; ok {
			return v, nil
		}
		return "", fmt.Errorf("response header %q not found", rest)
	case "meta":
		if v, ok := resp.Meta[rest]; ok {
			return v, nil
		}
		return "", fmt.Errorf("NATS meta header %q not found", rest)
	default:
		return "", fmt.Errorf("unknown extract prefix %q (valid: response, body, header, status, meta)", prefix)
	}
}

// extractJSON parses body as JSON and walks the slash-separated keys.
// Empty rest returns the whole body as a string. A non-object intermediate
// node produces a helpful error.
func extractJSON(body []byte, rest string) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("response body is empty")
	}

	// If the user asked for the whole body, return it raw.
	if rest == "" {
		return string(body), nil
	}

	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", fmt.Errorf("response body is not valid JSON: %w", err)
	}

	keys := strings.Split(rest, "/")
	cur := root
	for i, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("path %q: intermediate node at %q is not a JSON object", rest, strings.Join(keys[:i], "/"))
		}
		next, present := obj[k]
		if !present {
			return "", fmt.Errorf("path %q: key %q not found", rest, k)
		}
		cur = next
	}

	return toString(cur), nil
}

// toString converts a generic JSON value to its string form. We accept
// scalars (string, bool, numeric) directly; objects and arrays are
// re-marshalled so the caller still gets meaningful output if they want it.
func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// JSON numbers come back as float64. Avoid scientific notation for
		// integer-valued floats so "chargeId: 1000" round-trips cleanly.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		// Fall back: marshal back to JSON.
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// ExtractBodyFields extracts a list of fields from a raw JSON body and
// returns them in a map ready to be used as the .extracted namespace.
// This is the helper used by the mock server.
func ExtractBodyFields(body []byte, fields []ExtractField) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		v, err := extractJSON(body, f.Path)
		if err != nil {
			return nil, fmt.Errorf("extract %q: %w", f.Field, err)
		}
		out[f.Field] = v
	}
	return out, nil
}

// ExtractField is a minimal field/path pair. We avoid depending on the
// config package here so internal/template can be imported without pulling
// in TOML parsing.
type ExtractField struct {
	Field string
	Path  string
}
