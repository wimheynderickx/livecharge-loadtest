package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ManualPager is a standalone bubbletea Model that displays a pre-rendered
// markdown document in a scrollable viewport. Used by both the
// `loadtest manual` sub-command and the in-dashboard manual modal.
type ManualPager struct {
	vp      viewport.Model
	content string

	// width/height are stored so we can re-size the viewport when the
	// terminal is resized.
	width, height int
}

// NewManualPager builds a standalone pager around the supplied content.
// Use this when the manual is the only thing on screen (the `manual`
// sub-command). For the in-dashboard modal, see ManualModal below.
func NewManualPager(content string) ManualPager {
	return ManualPager{content: content}
}

// Init is the bubbletea entry point.
func (m ManualPager) Init() tea.Cmd { return nil }

// Update handles window resize, scroll keys, and the quit signal.
func (m ManualPager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = viewport.New(m.width, m.height-2) // 2 rows for header+footer
		m.vp.SetContent(m.content)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// View renders the pager: a header strip, the viewport, and a footer hint.
func (m ManualPager) View() string {
	if m.width == 0 {
		return "loading manual…"
	}
	header := RenderHeader(m.width)
	footer := RenderFooter(m.width, manualFooterHints(m.vp))
	return strings.Join([]string{header, m.vp.View(), footer}, "\n")
}

// manualFooterHints shows scroll position plus the most useful keys.
func manualFooterHints(vp viewport.Model) string {
	return fmt.Sprintf("[↑↓/jk] scroll  [PgUp/PgDn] page  [g/G] top/bottom  [q/esc] close  —  %3.0f%%",
		vp.ScrollPercent()*100)
}

// --- in-dashboard modal -----------------------------------------------------

// ManualModal is the variant used inside the live dashboard. It wraps a
// viewport but exposes an "open/close" lifecycle instead of being its own
// bubbletea program. The dashboard's app.go embeds one and routes keys.
type ManualModal struct {
	vp      viewport.Model
	content string
	open    bool
	ready   bool // true once we've received a WindowSizeMsg
}

// NewManualModal builds an unopened modal carrying the supplied content.
// Content is rendered once at construction; toggling open does not re-render.
func NewManualModal(content string) ManualModal {
	return ManualModal{content: content}
}

// Open shows the modal and resets the scroll position to the top.
func (m *ManualModal) Open() {
	m.open = true
	if m.ready {
		m.vp.GotoTop()
	}
}

// Close hides the modal.
func (m *ManualModal) Close() { m.open = false }

// IsOpen reports whether the modal is currently visible — the dashboard
// uses this to decide whether keys should reach the modal or the main view.
func (m *ManualModal) IsOpen() bool { return m.open }

// Resize is called by the dashboard on every tea.WindowSizeMsg so the
// viewport stays the right size when the terminal is resized.
func (m *ManualModal) Resize(width, height int) {
	if !m.ready {
		m.vp = viewport.New(width, height)
		m.vp.SetContent(m.content)
		m.ready = true
		return
	}
	m.vp.Width = width
	m.vp.Height = height
}

// HandleKey routes a key press to the modal. Returns whether the modal
// consumed the key (so the caller can stop further processing) and the
// cmd produced by the inner viewport.
func (m *ManualModal) HandleKey(msg tea.KeyMsg) (consumed bool, cmd tea.Cmd) {
	if !m.open {
		return false, nil
	}
	switch msg.String() {
	case "m", "esc":
		m.Close()
		return true, nil
	}
	m.vp, cmd = m.vp.Update(msg)
	return true, cmd
}

// View returns the modal's render output. Empty when closed.
//
// We deliberately return ONLY the viewport content — the dashboard's
// own footer (rendered in app.go) carries the scroll hints, so an inner
// hint line here would duplicate it.
func (m *ManualModal) View(width, contentHeight int) string {
	if !m.open || !m.ready {
		return ""
	}
	return m.vp.View()
}

// ScrollPercent exposes the viewport's scroll position so the dashboard
// footer can show "  47%" alongside the key hints.
func (m *ManualModal) ScrollPercent() float64 {
	if !m.ready {
		return 0
	}
	return m.vp.ScrollPercent()
}
