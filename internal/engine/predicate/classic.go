package predicate

import (
	"fmt"
	"strconv"
	"strings"

	"livecharge/loadtest/internal/config"
)

// EvaluateClassic walks classic-op predicates (eq/ne/contains/gt/lt) and
// returns the first match. Kept verbatim from the old internal/template
// implementation; renamed so the predicate package can also host the
// expr-based dispatcher.
func EvaluateClassic(preds []config.PredicateConfig, session map[string]any) Result {
	for _, p := range preds {
		if matchesClassic(p, session) {
			return Result{Matched: p.Name, NextStep: p.NextStep, HasMatch: true}
		}
	}
	return Result{}
}

// matchesClassic tests one classic-op predicate against the session.
func matchesClassic(p config.PredicateConfig, session map[string]any) bool {
	lhs := classicLookup(p.Field, session)
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
	}
	return false
}

// classicLookup resolves a field reference like the old template.lookup.
// "status" maps to session["status"]; "session.X" maps to session["X"];
// anything else is returned literally.
func classicLookup(field string, session map[string]any) string {
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

// numericCompare parses both sides as float64 and applies cmp.
// Returns false if either side fails to parse.
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
