package tui

import (
	"fmt"
	"strings"

	"livecharge/loadtest/internal/engine"
)

// renderLatency draws the ASCII bar-chart histogram for the currently
// selected scenario. Edges and labels come from the runner (resolved from
// the scenario's [metrics] configuration at startup); they're queried each
// render so a Restart that changed the config would pick up immediately.
//
// Layout per row:
//
//	  0–66 ms   ████████████████████████████  68.4%
//	 66–133 ms  ████████                       19.2%
//	133–200 ms  ████                            8.3%
func renderLatency(r *engine.Runner, width int) string {
	edges, labels := r.LatencyBuckets()
	counts := r.Buckets(edges)
	var total int64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return StyleMuted.Render("(no latency data yet — start the scenario or wait for replies)")
	}

	// Available bar width = total minus the label column (already padded
	// to a uniform width) and the trailing percentage column.
	labelW := 0
	if len(labels) > 0 {
		labelW = visualWidth(labels[0])
	}
	barWidth := width - labelW - 10
	if barWidth < 10 {
		barWidth = 10
	}

	var maxCount int64
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}

	var b strings.Builder
	b.WriteString(StyleKpiLabel.Render("LATENCY DISTRIBUTION") + "\n\n")
	for i, c := range counts {
		fraction := float64(c) / float64(total)
		barLen := 0
		if maxCount > 0 {
			barLen = int(float64(c) / float64(maxCount) * float64(barWidth))
		}
		bar := strings.Repeat("█", barLen)
		label := labels[i]
		b.WriteString(fmt.Sprintf("%s %s %5.1f%%\n", label, bar, fraction*100))
	}
	return b.String()
}

// visualWidth counts runes in s. We use it to size the bar column based on
// the longest label, which contains an en-dash (a single rune that takes 3
// bytes in UTF-8).
func visualWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
