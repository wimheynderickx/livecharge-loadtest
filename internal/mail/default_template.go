package mail

// DefaultBodyTemplate is the plain-text email body used when the user
// hasn't supplied a custom template. It covers the same information the
// TUI's Overview / Latency / Predicates tabs surface so a recipient can
// read the email and have a complete picture of the run.
//
// Placeholders live on TemplateContext (see template.go). The template is
// rendered with text/template, which means {{.Field}} and {{range ...}}
// work as expected. Whitespace control ({{- and -}}) is used to keep the
// rendered output tidy.
const DefaultBodyTemplate = `=== Sent because: {{.Trigger}} ===

Scenario:    {{.Scenario.Name}}
Description: {{.Scenario.Description}}
State:       {{.State}}
Started:     {{.StartedAt.Format "2006-01-02 15:04:05 MST"}}
Finished:    {{.FinishedAt.Format "2006-01-02 15:04:05 MST"}}
Elapsed:     {{.Elapsed}}

=== OVERVIEW ===
Sent:        {{.Sent}}
Received:    {{.Received}}
Errors:      {{.Errors}}

Throughput (msg/sec)
  current:   {{printf "%.1f" .MsgPerSec}}
  max:       {{printf "%.1f" .MaxMsgPerSec}}
  avg:       {{printf "%.1f" .AvgMsgPerSec}}

=== LATENCY ===
{{range $key, $val := .Latency -}}
  {{printf "%-7s" $key}} {{$val}}
{{end}}
{{- if .Histogram}}
=== LATENCY HISTOGRAM ===
{{range .Histogram -}}
  {{printf "%-18s %10d  %6.2f%%" .Label .Count .Pct}}
{{end}}
{{- end}}
{{- if .Predicates}}
=== PREDICATES ===
{{range .Predicates -}}
  {{printf "%-20s count=%d (%.1f%%)  p50=%s  p95=%s  p99=%s" .Name .Count .Pct .P50 .P95 .P99}}
{{end}}
{{- end}}
--
Livecharge OCS LoadTest
`
