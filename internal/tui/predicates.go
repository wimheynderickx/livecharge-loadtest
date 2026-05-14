package tui

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"livecharge/loadtest/internal/metrics"
)

// renderPredicates draws the predicate accounting table for the selected
// scenario. Rows are sorted alphabetically so the order is stable across
// snapshots.
func renderPredicates(s metrics.Snapshot) string {
	if len(s.Predicates) == 0 {
		return StyleMuted.Render("(no predicates have matched yet)")
	}

	names := make([]string, 0, len(s.Predicates))
	for n := range s.Predicates {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(StyleKpiLabel.Render("PREDICATE ACCOUNTING") + "\n\n")

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "name\tcount\tp50\tp95\tp99")
	for _, n := range names {
		ps := s.Predicates[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			n,
			formatCount(ps.Count),
			metrics.FormatLatency(ps.Percentiles[50]),
			metrics.FormatLatency(ps.Percentiles[95]),
			metrics.FormatLatency(ps.Percentiles[99]),
		)
	}
	tw.Flush()
	return b.String()
}
