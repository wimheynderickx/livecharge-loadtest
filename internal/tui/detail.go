package tui

import (
	"strings"

	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
	"livecharge/loadtest/internal/metrics"
)

// renderDetail draws the right pane: tab bar on top, active tab body below.
//
// width/height is the size available for this pane only (already minus the
// sidebar + gap). bodyHeight is height-2 to leave room for the tab bar and
// a one-row gap.
//
// scenarioPath is forwarded to the Overview tab so it can show the source
// file under the scenario description. Empty when unknown.
//
// mailStatus is the post-run email status for the active scenario. It may
// be nil when the email feature is disabled or no provider was wired —
// renderOverview hides the email row in that case.
func renderDetail(active Tab, snap metrics.Snapshot, runner *engine.Runner, scenarioPath string, logBuf *LogBuffer, mailStatus *mail.Status, width, height int) string {
	tabs := renderTabs(active)
	bodyHeight := height - 2
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	var body string
	switch active {
	case TabOverview:
		body = renderOverview(snap, runner, scenarioPath, mailStatus, width)
	case TabLatency:
		body = renderLatency(runner, width)
	case TabPredicates:
		body = renderPredicates(snap)
	case TabLog:
		body = logBuf.View(bodyHeight)
	}

	return strings.Join([]string{tabs, "", body}, "\n")
}
