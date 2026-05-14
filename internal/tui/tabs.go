package tui

import "strings"

// Tab is an enum over the detail-panel tabs.
type Tab int

const (
	TabOverview Tab = iota
	TabLatency
	TabPredicates
	TabLog
)

// tabLabels lists each tab in display order. The key shown in brackets is
// the keyboard shortcut.
var tabLabels = []string{
	"[1] Overview",
	"[2] Latency",
	"[3] Predicates",
	"[4] Log",
}

// renderTabs draws the tab bar across the top of the detail panel.
func renderTabs(active Tab) string {
	parts := make([]string, 0, len(tabLabels))
	for i, label := range tabLabels {
		if Tab(i) == active {
			parts = append(parts, StyleTabActive.Render(label))
		} else {
			parts = append(parts, StyleTabInactive.Render(label))
		}
	}
	return strings.Join(parts, " ")
}
