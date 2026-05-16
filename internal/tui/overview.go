package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
	"livecharge/loadtest/internal/metrics"
)

// renderOverview composes the Overview tab content.
//
// Stack (top to bottom):
//
//  1. Header: scenario name (accent, bold), description (white), file
//     path (muted grey).
//  2. Counter KPI row    — SENT / RECEIVED / ERRORS / PROTOCOL
//  3. Throughput KPI row — MSG/SEC (current) / MAX / AVG
//  4. Latency percentile list (one line per configured percentile)
//  5. Progress bar (when [load] total_messages is set)
//
// scenarioPath is the absolute filename the scenario was loaded from,
// rendered under the description so users can tell two similarly-named
// scenarios apart. Empty when the path is unknown.
//
// When the runner is in StateScriptError only a single red error line is
// shown — the KPI blocks have no meaningful content in that state.
func renderOverview(s metrics.Snapshot, runner *engine.Runner, scenarioPath string, mailStatus *mail.Status, width int) string {
	if runner != nil && runner.State() == engine.StateScriptError {
		msg := runner.ScriptError()
		if msg == "" {
			msg = "(no detail)"
		}
		return StyleErr.Render("SCRIPT ERROR — " + msg)
	}

	header := renderScenarioHeader(s, scenarioPath, width)
	counters := renderCounterRow(s)
	throughput := renderThroughputRow(s)
	percentiles := renderPercentiles(s)
	progress := renderProgress(s, width)

	parts := []string{header, "", counters, throughput, "", percentiles}
	if progress != "" {
		parts = append(parts, "", progress)
	}
	if line := renderMailRow(mailStatus); line != "" {
		parts = append(parts, "", line)
	}
	return strings.Join(parts, "\n")
}

// renderMailRow renders the Overview tab's email block. Returns "" when
// email is disabled for this scenario.
//
// Two lines are produced:
//
//  1. Config line — "EMAIL  on: start, progress (every 10s), done, error"
//     The most-recently-fired trigger is highlighted so the user can see
//     which event the status line below belongs to.
//  2. Status line — only when a send has been attempted. Shows pending /
//     sent / failed coloured appropriately, with the trigger reason in
//     parentheses.
//
// The "configured but no send yet" case shows only line 1.
func renderMailRow(status *mail.Status) string {
	if status == nil {
		return ""
	}
	snap := status.Snapshot()
	if snap.State == mail.StateDisabled {
		return ""
	}
	label := StyleKpiLabel.Render("EMAIL")
	cfg := formatMailTriggers(snap)
	stat := formatMailStatus(snap)

	// Indent the status line so it visually attaches under the config
	// line rather than reading as a sibling KPI.
	if stat == "" {
		return label + "  " + cfg
	}
	pad := strings.Repeat(" ", lipgloss.Width(label)+2)
	return label + "  " + cfg + "\n" + pad + stat
}

// formatMailTriggers renders the configured trigger list with the
// progress cadence inlined ("progress (every 10s)") and the last-fired
// trigger highlighted in bold accent so the status line below has a
// visual anchor.
func formatMailTriggers(snap mail.Snapshot) string {
	if len(snap.Triggers) == 0 {
		return StyleMuted.Render("(no triggers configured)")
	}
	active := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	parts := make([]string, 0, len(snap.Triggers))
	for _, t := range snap.Triggers {
		label := t
		if t == "progress" && snap.ReportInterval > 0 {
			label = "progress (every " + snap.ReportInterval.String() + ")"
		}
		if t == snap.Trigger {
			label = active.Render(label)
		}
		parts = append(parts, label)
	}
	return StyleMuted.Render("on: ") + strings.Join(parts, ", ")
}

// formatMailStatus renders the per-send state. Returns "" when the
// status hasn't progressed past Configured (no fire yet).
func formatMailStatus(snap mail.Snapshot) string {
	suffix := ""
	if snap.Trigger != "" {
		suffix = " (" + snap.Trigger + ")"
	}
	switch snap.State {
	case mail.StatePending:
		return StyleMuted.Render("📧 sending" + suffix + " to " + snap.Recipient + "…")
	case mail.StateSent:
		ok := lipgloss.NewStyle().Foreground(ColorOK)
		return ok.Render("📧 sent" + suffix + " to " + snap.Recipient + " at " + snap.FinishAt.Format("15:04:05"))
	case mail.StateFailed:
		errMsg := "unknown error"
		if snap.Err != nil {
			errMsg = snap.Err.Error()
		}
		return StyleErr.Render("📧 FAILED" + suffix + " — " + errMsg)
	}
	return ""
}

// renderScenarioHeader renders three stacked lines:
//
//   - scenario name in accent colour, bold;
//   - description in pure white (when present);
//   - source file path in muted grey (when known).
//
// PROTOCOL used to live here as a fourth line — it now sits next to the
// counter KPIs as a boxed value so users can compare it visually with
// SENT / RECEIVED / ERRORS.
func renderScenarioHeader(s metrics.Snapshot, scenarioPath string, width int) string {
	name := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent).Render(s.ScenarioName)
	parts := []string{name}
	if s.ScenarioDescription != "" {
		desc := lipgloss.NewStyle().Foreground(ColorWhite).Render(s.ScenarioDescription)
		parts = append(parts, desc)
	}
	if scenarioPath != "" {
		// Manually hard-wrap long paths and re-apply the muted style to
		// each visual line. The outer detail container does its own
		// width-constrained wrap of the composed view, and that outer wrap
		// would drop the inner ANSI style on wrapped continuation lines —
		// they'd render in the terminal's default foreground (white).
		// Splitting up front and styling each line independently avoids
		// relying on the outer wrapper to carry foreground colour forward.
		for _, line := range hardWrap(scenarioPath, width) {
			parts = append(parts, StyleMuted.Render(line))
		}
	}
	return strings.Join(parts, "\n")
}

// hardWrap breaks s into chunks no wider than width runes. width <= 0
// disables wrapping (the original string is returned unchanged). Used
// for content with no natural break points (file paths) where word-wrap
// would be a no-op.
func hardWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	if len(runes) <= width {
		return []string{s}
	}
	out := make([]string, 0, (len(runes)+width-1)/width)
	for i := 0; i < len(runes); i += width {
		end := i + width
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// renderCounterRow renders the SENT / RECEIVED / ERRORS / PROTOCOL boxes.
// PROTOCOL shares the same boxed style as the counters so it reads as a
// peer; when the transport hasn't reported a protocol yet the box shows
// a muted dash.
func renderCounterRow(s metrics.Snapshot) string {
	proto := s.Protocol
	if proto == "" {
		proto = "—"
	}
	boxes := []string{
		kpiBox("SENT", formatCount(s.Sent), lipgloss.NoColor{}),
		kpiBox("RECEIVED", formatCount(s.Received), lipgloss.NoColor{}),
		kpiBox("ERRORS", formatCount(s.Errors), maybeError(s.Errors)),
		kpiBox("PROTOCOL", proto, lipgloss.NoColor{}),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, boxes...)
}

// renderThroughputRow renders the current/max/avg msg/sec boxes.
func renderThroughputRow(s metrics.Snapshot) string {
	boxes := []string{
		kpiBox("MSG/SEC", fmt.Sprintf("%.1f", s.MsgPerSec), lipgloss.NoColor{}),
		kpiBox("MAX /S", fmt.Sprintf("%.1f", s.MaxMsgPerSec), lipgloss.NoColor{}),
		kpiBox("AVG /S", fmt.Sprintf("%.1f", s.AvgMsgPerSec), lipgloss.NoColor{}),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, boxes...)
}

func kpiBox(label, value string, valueColor lipgloss.TerminalColor) string {
	valueStyle := StyleKpiValue.Copy()
	if _, ok := valueColor.(lipgloss.NoColor); !ok {
		valueStyle = valueStyle.Foreground(valueColor)
	}
	body := StyleKpiLabel.Render(label) + "\n" + valueStyle.Render(value)
	return StyleKpiBox.Render(body)
}

// maybeError colours an error count red when non-zero.
func maybeError(n int64) lipgloss.TerminalColor {
	if n > 0 {
		return ColorError
	}
	return lipgloss.NoColor{}
}

// renderPercentiles shows the configured percentile distribution in a
// human-friendly table. Order is preserved by sorting keys ascending so the
// list reads naturally (p50 first, p99.9 last).
func renderPercentiles(s metrics.Snapshot) string {
	if len(s.Percentiles) == 0 {
		return StyleMuted.Render("(no latency data yet)")
	}
	keys := make([]float64, 0, len(s.Percentiles))
	for k := range s.Percentiles {
		keys = append(keys, k)
	}
	sort.Float64s(keys)

	yellow := lipgloss.NewStyle().Foreground(ColorLatencyValue)

	var b strings.Builder
	b.WriteString(StyleKpiLabel.Render("LATENCY") + "\n")
	for _, k := range keys {
		num, unit := splitLatency(metrics.FormatLatency(s.Percentiles[k]))
		b.WriteString(fmt.Sprintf("  p%-5s %7s%s\n",
			formatPercentile(k), yellow.Render(num), unit))
	}
	return b.String()
}

// splitLatency splits a FormatLatency result such as "0.135 ms" into
// the numeric part "0.135" and the unit part " ms". The unit is always
// returned with the leading space so callers can concatenate directly.
func splitLatency(s string) (number, unit string) {
	i := strings.LastIndex(s, " ")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// formatPercentile renders 50 as "50", 99.9 as "99.9".
func formatPercentile(p float64) string {
	if p == float64(int(p)) {
		return fmt.Sprintf("%d", int(p))
	}
	return fmt.Sprintf("%g", p)
}

// renderProgress draws an ASCII progress bar when a total-messages target
// is known. Otherwise returns "".
func renderProgress(s metrics.Snapshot, width int) string {
	if s.TotalTarget <= 0 {
		return ""
	}
	pct := float64(s.Sent) / float64(s.TotalTarget)
	if pct > 1 {
		pct = 1
	}
	// Build the label first so its exact rune width drives the bar size,
	// preventing the line from wrapping when counts contain many digits.
	label := fmt.Sprintf(" %3.0f%% (%s / %s)",
		pct*100, formatCount(s.Sent), formatCount(s.TotalTarget))
	barWidth := width - len(label)
	if barWidth < 4 {
		barWidth = 4
	}
	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + label
}
