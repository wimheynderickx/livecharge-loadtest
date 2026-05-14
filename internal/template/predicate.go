package template

import (
	"fmt"
	"strconv"
	"strings"

	"livecharge/loadtest/internal/config"
)

// PredicateResult describes the outcome of evaluating a list of predicates
// against a session map.
type PredicateResult struct {
	// Matched is the name of the predicate that fired, or "" if none did.
	Matched string
	// NextStep is the step name to jump to next. Only meaningful when
	// Matched is non-empty. "" means "end the session".
	NextStep string
	// HasMatch is true when any predicate fired. We carry this separately
	// because NextStep == "" is itself a valid signal ("end session").
	HasMatch bool
}

// Evaluate walks the predicates in order and returns the first match.
// The session map is the .session namespace populated by the extractor.
// Field references use the "session.<key>" prefix; "status" is reserved
// for the HTTP status code, which the caller injects into session under
// that key.
func Evaluate(preds []config.PredicateConfig, session map[string]any) PredicateResult {
	for _, p := range preds {
		if matches(p, session) {
			return PredicateResult{
				Matched:  p.Name,
				NextStep: p.NextStep,
				HasMatch: true,
			}
		}
	}
	return PredicateResult{}
}

// matches tests one predicate against the session.
func matches(p config.PredicateConfig, session map[string]any) bool {
	lhs := lookup(p.Field, session)

	switch p.Op {
	case "eq":
		return lhs == p.Value
	case "ne":
		return lhs != p.Value
	case "contains":
		return strings.Contains(lhs, p.Value)
	case "gt":
		return numericCompare(lhs, p.Value, func(a, b float64) bool { return a > b })
	case "lt":
		return numericCompare(lhs, p.Value, func(a, b float64) bool { return a < b })
	default:
		// Unknown op: validate.go should have rejected this earlier.
		return false
	}
}

// lookup resolves a field reference into a string. Recognised forms:
//
//	session.X — look up key X in the session map
//	status    — short-hand for session["status"]
//	anything else — returned literally (useful for explicit constants)
//
// Values are converted to their string form using fmt %v so callers don't
// have to think about JSON-derived float64s.
func lookup(field string, session map[string]any) string {
	if field == "status" {
		v, ok := session["status"]
		if !ok {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	if strings.HasPrefix(field, "session.") {
		key := strings.TrimPrefix(field, "session.")
		v, ok := session[key]
		if !ok {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	return field
}

// numericCompare parses both sides as float64 and applies cmp. If either
// side fails to parse, the predicate does not match.
func numericCompare(lhs, rhs string, cmp func(a, b float64) bool) bool {
	a, err := strconv.ParseFloat(lhs, 64)
	if err != nil {
		return false
	}
	b, err := strconv.ParseFloat(rhs, 64)
	if err != nil {
		return false
	}
	return cmp(a, b)
}
