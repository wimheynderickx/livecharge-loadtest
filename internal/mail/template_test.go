package mail

import (
	"strings"
	"testing"
	"time"
)

// sampleContext returns a TemplateContext populated with values that
// touch every section of the default template so unit tests can verify
// each branch renders.
func sampleContext() TemplateContext {
	return TemplateContext{
		Scenario: ScenarioInfo{
			Name:        "charge-flow",
			Description: "Happy/error mix",
			Path:        "/scenarios/charge-flow.toml",
		},
		State:        "DONE",
		Trigger:      "done",
		StartedAt:    time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		FinishedAt:   time.Date(2026, 5, 14, 10, 1, 30, 0, time.UTC),
		Elapsed:      "00:01:30",
		Sent:         100,
		Received:     99,
		Errors:       1,
		MsgPerSec:    50.5,
		MaxMsgPerSec: 60.2,
		AvgMsgPerSec: 48.0,
		Latency: map[string]string{
			"p50": "0.135 ms",
			"p95": "1.23 ms",
			"p99": "12 ms",
		},
		Histogram: []HistogramRow{
			{Label: "0-1 ms", Count: 80, Pct: 80.0},
			{Label: "1-10 ms", Count: 19, Pct: 19.0},
			{Label: "10+ ms", Count: 1, Pct: 1.0},
		},
		Predicates: []PredicateRow{
			{Name: "happy", Count: 90, Pct: 90.0, P50: "0.1 ms", P95: "1 ms", P99: "5 ms"},
			{Name: "error", Count: 10, Pct: 10.0, P50: "0.5 ms", P95: "2 ms", P99: "8 ms"},
		},
	}
}

func TestRenderText_Empty(t *testing.T) {
	out, err := RenderText("", sampleContext())
	if err != nil || out != "" {
		t.Fatalf("empty template should return empty string: out=%q err=%v", out, err)
	}
}

func TestRenderText_SubjectPlaceholders(t *testing.T) {
	out, err := RenderText("[{{.Scenario.Name}}] {{.State}}", sampleContext())
	if err != nil {
		t.Fatal(err)
	}
	if out != "[charge-flow] DONE" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderText_LatencyLookup(t *testing.T) {
	out, err := RenderText("p99={{.Latency.p99}}", sampleContext())
	if err != nil {
		t.Fatal(err)
	}
	if out != "p99=12 ms" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderText_DescriptionPlaceholder(t *testing.T) {
	// The spec explicitly requires that name AND description placeholders
	// work in the subject.
	out, err := RenderText("{{.Scenario.Description}}", sampleContext())
	if err != nil {
		t.Fatal(err)
	}
	if out != "Happy/error mix" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderText_MissingKeyErrors(t *testing.T) {
	// We use missingkey=error so typos surface as Status.Failed rather
	// than producing a half-rendered email.
	_, err := RenderText("{{.NopeNope}}", sampleContext())
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}

func TestRenderText_RangeOverPredicates(t *testing.T) {
	tmpl := "{{range .Predicates}}{{.Name}}={{.Count}};{{end}}"
	out, err := RenderText(tmpl, sampleContext())
	if err != nil {
		t.Fatal(err)
	}
	if out != "happy=90;error=10;" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderText_DefaultBodyRendersCleanly(t *testing.T) {
	out, err := RenderText(DefaultBodyTemplate, sampleContext())
	if err != nil {
		t.Fatalf("default template failed to render: %v", err)
	}
	// Spot-check that each section's heading + a key value is present.
	checks := []string{
		"Scenario:    charge-flow",
		"Description: Happy/error mix",
		"=== OVERVIEW ===",
		"=== LATENCY ===",
		"p50",
		"=== LATENCY HISTOGRAM ===",
		"0-1 ms",
		"=== PREDICATES ===",
		"happy",
		"Livecharge OCS LoadTest",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("default body missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderSubjectWithFallback_BadTemplate(t *testing.T) {
	// A typo in the subject template must fall back to a literal so the
	// email still ships — losing the email over a typo is worse than
	// losing a custom subject.
	got := RenderSubjectWithFallback("{{.NotAField}}", sampleContext())
	if !strings.Contains(got, "charge-flow") || !strings.Contains(got, "DONE") {
		t.Fatalf("fallback subject should include scenario name and state, got %q", got)
	}
}

func TestRenderSubjectWithFallback_DefaultTemplate(t *testing.T) {
	got := RenderSubjectWithFallback("", sampleContext())
	if !strings.Contains(got, "charge-flow") {
		t.Fatalf("default subject should include scenario name, got %q", got)
	}
}

func TestLatencyKeys_SortedNumerically(t *testing.T) {
	ctx := TemplateContext{Latency: map[string]string{
		"p99.9": "x", "p50": "x", "p95": "x", "p99": "x",
	}}
	keys := ctx.LatencyKeys()
	want := []string{"p50", "p95", "p99", "p99.9"}
	if len(keys) != len(want) {
		t.Fatalf("len mismatch: got %v want %v", keys, want)
	}
	for i := range keys {
		if keys[i] != want[i] {
			t.Errorf("position %d: got %q want %q", i, keys[i], want[i])
		}
	}
}
