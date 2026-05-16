package tui

import "github.com/charmbracelet/lipgloss"

// SidebarWidth is the column width of the left scenario list. Detail panes
// get the remaining width minus a one-column gap.
const SidebarWidth = 30

// Colours used across every component. Pulling them into one place keeps
// the dashboard consistent and makes a future light/dark variant a
// one-file change.
var (
	ColorAccent       = lipgloss.Color("#7C3AED")
	ColorRunning      = lipgloss.Color("#22C55E")
	ColorStopped      = lipgloss.Color("#F59E0B")
	ColorError        = lipgloss.Color("#EF4444")
	ColorMuted        = lipgloss.Color("#6B7280")
	ColorBorder       = lipgloss.Color("#374151")
	ColorOK           = lipgloss.Color("#34D399")
	ColorWhite        = lipgloss.Color("#FFFFFF") // pure white, e.g. scenario description
	ColorLatencyValue = lipgloss.Color("#FBBF24") // bright amber-yellow for numeric latency

	// Branding bar colours used by the top and bottom strips.
	ColorBarBg     = lipgloss.Color("#1E3A8A") // navy
	ColorBarFg     = lipgloss.Color("#93C5FD") // sky-300
	ColorBarAccent = lipgloss.Color("#FFFFFF") // pure white, for the product name
)

// Reusable styles. Most components compose these via Copy() so they don't
// mutate the shared instance.
var (
	StyleSidebar = lipgloss.NewStyle().
			Width(SidebarWidth).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(ColorBorder).
			BorderRight(true).
			Padding(0, 1)

	StyleDetail = lipgloss.NewStyle().
			Padding(0, 1)

	// StyleHeaderBar is the base style for the top branding bar.
	// RenderHeader composes "Livecharge OCS " (light blue) + "LoadTest"
	// (white) on top of this, then stretches to the full terminal width.
	StyleHeaderBar = lipgloss.NewStyle().
			Background(ColorBarBg).
			Foreground(ColorBarFg).
			Bold(true)

	StyleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorAccent).
			Padding(0, 1)

	StyleTabInactive = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Padding(0, 1)

	StyleSelectedRow = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorAccent)

	StyleMuted = lipgloss.NewStyle().Foreground(ColorMuted)

	StyleErr = lipgloss.NewStyle().Foreground(ColorError)

	StyleKpiBox = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1).
			MarginRight(1)

	StyleKpiLabel = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true)

	StyleKpiValue = lipgloss.NewStyle().
			Bold(true)

	StyleFooter = lipgloss.NewStyle().
			Background(ColorBarBg).
			Foreground(ColorBarFg).
			Padding(0, 1)
)

// RenderHeader builds the two-tone branding bar at the top of the dashboard.
// "Livecharge OCS " is rendered in the bar's light-blue colour; "LoadTest"
// in white. The result is stretched to `width` so the navy background fills
// the whole row.
func RenderHeader(width int) string {
	brand := lipgloss.NewStyle().
		Background(ColorBarBg).
		Foreground(ColorBarFg).
		Bold(true).
		Render("Livecharge OCS ")
	product := lipgloss.NewStyle().
		Background(ColorBarBg).
		Foreground(ColorBarAccent).
		Bold(true).
		Render("LoadTest")
	return StyleHeaderBar.Copy().
		Width(width).
		Padding(0, 1).
		Render(brand + product)
}

// RenderFooter stretches the footer hint line to `width` so the navy
// background fills the row end to end.
func RenderFooter(width int, text string) string {
	return StyleFooter.Copy().Width(width).Render(text)
}

// StateBadge returns a coloured one-token badge for the given state name.
// The state names match engine.State.String() output.
func StateBadge(state string) string {
	switch state {
	case "RUNNING":
		return lipgloss.NewStyle().Foreground(ColorRunning).Render("● RUNNING")
	case "STOPPED":
		return lipgloss.NewStyle().Foreground(ColorStopped).Render("■ STOPPED")
	case "DONE":
		return lipgloss.NewStyle().Foreground(ColorOK).Render("✓ DONE")
	case "ERROR":
		return lipgloss.NewStyle().Foreground(ColorError).Render("✗ ERROR")
	case "SCRIPT_ERROR":
		return lipgloss.NewStyle().Foreground(ColorError).Render("✗ SCRIPT ERR")
	case "IDLE":
		return StyleMuted.Render("○ IDLE")
	default:
		return StyleMuted.Render(state)
	}
}
