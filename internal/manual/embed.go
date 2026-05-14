// Package manual exposes the operational manual that ships with the
// loadtest binary.
//
// manual.md is the canonical source. The TUI `m` shortcut, the
// `loadtest manual` sub-command, and `loadtest help` all read from it.
// To update the user-facing documentation, edit manual.md directly.
package manual

import (
	_ "embed"

	"github.com/charmbracelet/glamour"
)

//go:embed manual.md
var raw string

// Markdown returns the manual's source markdown. Callers writing to a
// non-TTY (CI, piped output) should use this and let the receiving tool
// decide how to render.
func Markdown() string { return raw }

// Render returns the manual rendered for display in a terminal. width is
// the column count used for wrapping; pass the terminal width when known.
//
// The "dark" style ships with glamour and works on both light- and
// dark-background terminals because it uses high-contrast accents rather
// than a fixed background colour.
//
// On any rendering error we fall back to the raw markdown so the user
// still sees something useful.
func Render(width int) string {
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return raw
	}
	out, err := r.Render(raw)
	if err != nil {
		return raw
	}
	return out
}
