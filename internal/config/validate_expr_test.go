package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidate_ExprOpAccepted(t *testing.T) {
	cfg := &ScenarioConfig{
		Scenario:  ScenarioMeta{Name: "t"},
		Transport: TransportConfig{Type: "http", URL: "http://localhost"},
		Load: LoadConfig{
			TotalMessages:   1,
			Concurrency:     1,
			ResponseTimeout: Duration{time.Second},
		},
		Steps: []StepConfig{{
			Name:     "s1",
			Method:   "POST",
			Path:     "/",
			Template: "{}",
			Predicates: []PredicateConfig{
				{Name: "p", Op: "expr", Value: `status == 200`},
			},
		}},
	}
	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("op=expr should validate; got errors: %v", errs)
	}
}

func TestValidate_ExprOpEmptyValueRejected(t *testing.T) {
	cfg := scenarioWithPredicate(t, PredicateConfig{Name: "p", Op: "expr", Value: ""})
	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Section, "predicate") && strings.Contains(e.Message, "value") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected op=expr + empty value to error; got: %v", errs)
	}
}

func TestValidate_ExprOpWithFieldWarns(t *testing.T) {
	cfg := scenarioWithPredicate(t, PredicateConfig{
		Name: "p", Op: "expr", Value: `status == 200`, Field: "status",
	})
	warns := ValidateWarnings(cfg)
	found := false
	for _, w := range warns {
		if strings.Contains(w.Field, "predicate") && strings.Contains(w.Message, "field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning that field is unused with op=expr; got: %v", warns)
	}
}

// scenarioWithPredicate wraps a single predicate in a minimal valid scenario.
func scenarioWithPredicate(t *testing.T, p PredicateConfig) *ScenarioConfig {
	t.Helper()
	return &ScenarioConfig{
		Scenario:  ScenarioMeta{Name: "t"},
		Transport: TransportConfig{Type: "http", URL: "http://localhost"},
		Load: LoadConfig{
			TotalMessages:   1,
			Concurrency:     1,
			ResponseTimeout: Duration{time.Second},
		},
		Steps: []StepConfig{{
			Name: "s1", Method: "POST", Path: "/", Template: "{}",
			Predicates: []PredicateConfig{p},
		}},
	}
}
