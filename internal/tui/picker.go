package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// scenarioPicker is a vertical list picker used by the 'a' (add scenario)
// shortcut. We hand-roll it instead of bubbles/list to keep the code
// short and to render in the same Lip Gloss style as the rest of the
// dashboard — the bubbles widget brings its own styling and filtering
// machinery that we don't need here.
type scenarioPicker struct {
	open     bool
	title    string
	items    []ScenarioCandidate
	selected int
	// status is shown under the list. Set when an add attempt fails so
	// the user can see the error without closing the picker.
	status string
}

// Open populates the picker with `items` and shows it. Caller passes a
// title (e.g. "Add scenario") which appears at the top of the modal.
func (p *scenarioPicker) Open(title string, items []ScenarioCandidate) {
	p.title = title
	p.items = items
	p.selected = 0
	p.status = ""
	p.open = true
}

// Close hides the picker without selecting anything.
func (p *scenarioPicker) Close() { p.open = false }

// IsOpen reports whether the picker is currently visible.
func (p *scenarioPicker) IsOpen() bool { return p.open }

// Move shifts the selection by delta with wrap-around.
func (p *scenarioPicker) Move(delta int) {
	if len(p.items) == 0 {
		return
	}
	p.selected = (p.selected + delta + len(p.items)) % len(p.items)
}

// Selected returns the highlighted candidate, or zero-value + false when
// the list is empty.
func (p *scenarioPicker) Selected() (ScenarioCandidate, bool) {
	if p.selected < 0 || p.selected >= len(p.items) {
		return ScenarioCandidate{}, false
	}
	return p.items[p.selected], true
}

// SetStatus sets a footer message under the list (typically an error).
func (p *scenarioPicker) SetStatus(s string) { p.status = s }

// View renders the picker centred inside the given content area.
func (p *scenarioPicker) View(width, height int) string {
	if !p.open {
		return ""
	}

	if len(p.items) == 0 {
		return modalFrame(width, height,
			p.title,
			StyleMuted.Render("(no candidates — every scanned scenario is already loaded)"),
			"[b] browse files  [esc] close",
		)
	}

	maxRows := height - 6 // title + 2 separators + status + hint + padding
	if maxRows < 3 {
		maxRows = 3
	}

	start, end := windowAround(p.selected, len(p.items), maxRows)

	var rows []string
	for i := start; i < end; i++ {
		item := p.items[i]
		row := fmt.Sprintf("%-30s  %s", truncate(item.Name, 30), truncate(item.Description, width-50))
		if i == p.selected {
			row = StyleSelectedRow.Render("▶ " + row)
		} else {
			row = "  " + row
		}
		rows = append(rows, row)
	}

	pathLine := StyleMuted.Render("  " + p.items[p.selected].Path)
	body := strings.Join(rows, "\n") + "\n\n" + pathLine
	if p.status != "" {
		body += "\n" + StyleErr.Render("  "+p.status)
	}
	return modalFrame(width, height, p.title, body, "[↑↓] move  [enter] add  [b] browse files  [esc] cancel")
}

// modalFrame is a shared layout for the picker / confirm / filebrowser
// modals: a bordered box with a title bar at the top and a hint line
// at the bottom.
func modalFrame(width, height int, title, body, hint string) string {
	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2)

	innerWidth := width - 6
	if innerWidth < 30 {
		innerWidth = 30
	}

	titleLine := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent).Render(title)
	hintLine := StyleMuted.Render(hint)
	content := titleLine + "\n\n" + body + "\n\n" + hintLine
	return lipgloss.Place(
		width, height, lipgloss.Center, lipgloss.Center,
		border.Width(innerWidth).Render(content),
	)
}

// truncate clips s to at most n runes, appending an ellipsis when it
// shortens. Used by the picker to keep rows on a single line.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

// windowAround returns a [start, end) range of size at most maxRows
// centred on `selected` within [0, length).
func windowAround(selected, length, maxRows int) (int, int) {
	if length <= maxRows {
		return 0, length
	}
	half := maxRows / 2
	start := selected - half
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > length {
		end = length
		start = end - maxRows
	}
	return start, end
}
