package mail

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"
	"time"
)

// TemplateContext is the value passed to text/template when rendering the
// subject and body. Every field on this struct is referenceable as a
// placeholder; documented and stable so users can write templates against
// it without surprises.
//
// The map fields (Latency) use string keys so users can write
// {{.Latency.p99}} or range over them in any order.
type TemplateContext struct {
	// Scenario carries the test identity so subjects can reference the
	// name and description (a specific request from the design).
	Scenario ScenarioInfo

	// State is the terminal state name as the engine reports it: "DONE"
	// or "ERROR". Use Trigger for the lowercase event name.
	State string

	// Trigger is the lifecycle event that fired the send: "done" or
	// "error". Useful for branching in templates: {{if eq .Trigger "error"}}…{{end}}.
	Trigger string

	// StartedAt is when the scenario was first started.
	StartedAt time.Time

	// FinishedAt is when the scenario reached its terminal state. The
	// difference from StartedAt is the wall-clock duration of the run.
	FinishedAt time.Time

	// Elapsed is StartedAt..FinishedAt formatted as HH:MM:SS — handy for
	// subject lines where a duration is more useful than two timestamps.
	Elapsed string

	// === Tab 1 — Overview ===
	Sent         int64
	Received     int64
	Errors       int64
	MsgPerSec    float64
	MaxMsgPerSec float64
	AvgMsgPerSec float64

	// Latency maps percentile keys ("p50", "p95", "p99", "p99.9", ...) to
	// human-formatted strings ("0.135 ms", "12 ms"). The string form
	// matches what the TUI shows so the email is a faithful textual copy.
	Latency map[string]string

	// === Tab 2 — Latency histogram ===
	Histogram []HistogramRow

	// === Tab 3 — Predicates ===
	Predicates []PredicateRow
}

// ScenarioInfo carries the immutable identity of the scenario for use in
// templates. Path is included so emails can identify *which* file the run
// came from when several scenarios share a name.
type ScenarioInfo struct {
	Name        string
	Description string
	Path        string
}

// HistogramRow is one bucket of the latency histogram. Pct is 0..100.
type HistogramRow struct {
	Label string
	Count int64
	Pct   float64
}

// PredicateRow is one entry in the predicates table. Percentile values
// are pre-formatted strings so the template doesn't have to know about
// units.
type PredicateRow struct {
	Name  string
	Count int64
	Pct   float64
	P50   string
	P95   string
	P99   string
}

// RenderText executes tmplText against ctx and returns the bytes written.
// Used for both subject and body — text/template handles both fine. Returns
// the original text untouched when tmplText is empty.
//
// Errors include the template source location, which surfaces in the
// Status.MarkFailed message the TUI shows.
func RenderText(tmplText string, ctx TemplateContext) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	t, err := template.New("mail").Option("missingkey=error").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// RenderSubjectWithFallback runs RenderText against the subject template
// and, on failure, returns a literal fallback so the email still ships.
// This is a deliberate decision: a typo in the subject template should not
// prevent the recipient from learning the scenario finished.
func RenderSubjectWithFallback(tmpl string, ctx TemplateContext) string {
	if tmpl == "" {
		tmpl = DefaultSubject
	}
	out, err := RenderText(tmpl, ctx)
	if err == nil {
		return out
	}
	return fmt.Sprintf("[Livecharge] %s finished (%s)", ctx.Scenario.Name, ctx.State)
}

// LatencyKeys returns the latency map keys sorted ascending by their
// numeric value so iteration order is stable and human-readable
// (p50 before p99.9 etc.). Returns nil for an empty/missing map.
func (c TemplateContext) LatencyKeys() []string {
	if len(c.Latency) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c.Latency))
	for k := range c.Latency {
		keys = append(keys, k)
	}
	// Sort by stripping the leading 'p' and comparing as floats. We don't
	// expect malformed keys (the producer is internal) so a parse failure
	// just falls back to lexical order for that pair.
	sort.Slice(keys, func(i, j int) bool {
		ai, aj := parseLatencyKey(keys[i]), parseLatencyKey(keys[j])
		return ai < aj
	})
	return keys
}

// parseLatencyKey turns "p99.9" into 99.9. Used only for sort ordering.
func parseLatencyKey(k string) float64 {
	if len(k) < 2 || k[0] != 'p' {
		return 0
	}
	var f float64
	_, err := fmt.Sscanf(k[1:], "%f", &f)
	if err != nil {
		return 0
	}
	return f
}
