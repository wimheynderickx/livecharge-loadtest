package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"livecharge/loadtest/internal/metrics"
)

// renderSidebar produces the left-pane string given the current snapshots
// and the selected index.
//
// Layout per scenario (vertical):
//
//	▶ scenario-name        ← bold/highlighted when selected
//	  ● RUNNING
//	  sent 12,345  rate 250/s
//	  p99 24 ms
//
// A "suite totals" block sits at the bottom.
// linesPerSidebarRow is the number of terminal lines one scenario entry
// occupies: 4 content lines + 1 blank separator.
const linesPerSidebarRow = 5

// totalsBlockLines is the number of lines the "─── totals ───" block uses.
const totalsBlockLines = 6

func renderSidebar(snaps []metrics.Snapshot, selected int, height int) string {
	if len(snaps) == 0 {
		return StyleSidebar.Copy().Height(height).Render("(no scenarios)")
	}

	// How many scenario rows fit above the fixed totals block?
	// The blank line after the last visible row acts as the separator, so
	// the maths are: K rows × linesPerSidebarRow + totalsBlockLines ≤ height.
	maxVisible := (height - totalsBlockLines) / linesPerSidebarRow
	if maxVisible < 1 {
		maxVisible = 1
	}

	// Scroll the visible window so the selected entry is always in view.
	start, end := windowAround(selected, len(snaps), maxVisible)

	var rows []string
	for i := start; i < end; i++ {
		rows = append(rows, renderSidebarRow(snaps[i], i == selected))
		rows = append(rows, "") // blank separator
	}

	// Show a position indicator when the list is taller than the viewport.
	if len(snaps) > maxVisible {
		rows = append(rows,
			StyleMuted.Render(fmt.Sprintf("  %d / %d", selected+1, len(snaps))),
			"",
		)
	}

	// Suite totals — aggregated across ALL scenarios (not just the visible
	// window) so the numbers remain consistent regardless of scroll position.
	//
	// Aggregation rules:
	//   sent / errs / rate / maxRate — straight sums across scenarios
	//   avgRate — Σsent / max(elapsedMs) so a stale scenario doesn't drag
	//             the average down.
	var sent, errs, maxElapsedMs int64
	var rate, maxRate float64
	for _, s := range snaps {
		sent += s.Sent
		errs += s.Errors
		rate += s.MsgPerSec
		maxRate += s.MaxMsgPerSec
		if s.ElapsedMs > maxElapsedMs {
			maxElapsedMs = s.ElapsedMs
		}
	}
	var avgRate float64
	if maxElapsedMs > 0 {
		avgRate = float64(sent) / (float64(maxElapsedMs) / 1000.0)
	}
	y := lipgloss.NewStyle().Foreground(ColorLatencyValue)
	rows = append(rows,
		StyleMuted.Render("─── totals ───"),
		fmt.Sprintf("sent   %s", y.Render(formatCount(sent))),
		fmt.Sprintf("rate   %s/s", y.Render(fmt.Sprintf("%.1f", rate))),
		fmt.Sprintf("max/s  %s", y.Render(fmt.Sprintf("%.1f", maxRate))),
		fmt.Sprintf("avg/s  %s", y.Render(fmt.Sprintf("%.1f", avgRate))),
		fmt.Sprintf("err    %s", y.Render(formatCount(errs))),
	)

	body := strings.Join(rows, "\n")
	return StyleSidebar.Copy().Height(height).Render(body)
}

// renderSidebarRow renders a single scenario block in the sidebar.
func renderSidebarRow(s metrics.Snapshot, selected bool) string {
	name := s.ScenarioName
	if len(name) > SidebarWidth-4 {
		name = name[:SidebarWidth-5] + "…"
	}
	prefix := "  "
	style := lipgloss.NewStyle()
	if selected {
		prefix = "▶ "
		style = StyleSelectedRow
	}
	header := style.Render(prefix + name)

	p99 := s.Percentiles[99]
	return strings.Join([]string{
		header,
		"  " + StateBadge(s.StateName),
		fmt.Sprintf("  sent %s  rate %.0f/s", formatCount(s.Sent), s.MsgPerSec),
		fmt.Sprintf("  p99  %s", metrics.FormatLatency(p99)),
	}, "\n")
}

// formatCount renders large integers with thousands separators. The TUI
// has limited horizontal space, so 12345 reading as "12,345" is much
// faster than "12345" at a glance.
func formatCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert commas from the right.
	var b strings.Builder
	first := len(s) % 3
	if first > 0 {
		b.WriteString(s[:first])
		if len(s) > first {
			b.WriteByte(',')
		}
	}
	for i := first; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
