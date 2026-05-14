package tui

import (
	"strings"
)

// maxLogLines is the size of the per-scenario log ring buffer. Older lines
// are evicted as new ones arrive; 500 is roughly one full screen of context
// without consuming meaningful memory at typical event rates.
const maxLogLines = 500

// LogBuffer holds a bounded ring of log lines for one scenario.
//
// The buffer is filled by the app's Update loop, which drains the engine's
// log channel on every tick. Scrolling is per-buffer: each scenario keeps
// its own scroll offset so switching scenarios doesn't snap the view back
// to the bottom.
type LogBuffer struct {
	lines  []string
	scroll int // number of lines scrolled up from the bottom
}

// Append adds one line, evicting the oldest entry if the ring is full.
// The scroll offset is preserved so an active reader doesn't get yanked
// back to the bottom just because a new error came in.
func (b *LogBuffer) Append(line string) {
	b.lines = append(b.lines, line)
	if len(b.lines) > maxLogLines {
		// Drop oldest entries; if the reader had scrolled up, keep their
		// view stable by decrementing scroll proportionally.
		drop := len(b.lines) - maxLogLines
		b.lines = b.lines[drop:]
		b.scroll -= drop
		if b.scroll < 0 {
			b.scroll = 0
		}
	}
}

// ScrollUp moves the view towards older entries.
func (b *LogBuffer) ScrollUp(n int) {
	b.scroll += n
	if b.scroll > len(b.lines) {
		b.scroll = len(b.lines)
	}
}

// ScrollDown moves the view towards newer entries.
func (b *LogBuffer) ScrollDown(n int) {
	b.scroll -= n
	if b.scroll < 0 {
		b.scroll = 0
	}
}

// View renders the visible window. height is the number of rows available.
func (b *LogBuffer) View(height int) string {
	if len(b.lines) == 0 {
		return StyleMuted.Render("(no log entries yet)")
	}
	if height <= 0 {
		return ""
	}
	end := len(b.lines) - b.scroll
	if end < 0 {
		end = 0
	}
	start := end - height
	if start < 0 {
		start = 0
	}
	return strings.Join(b.lines[start:end], "\n")
}
