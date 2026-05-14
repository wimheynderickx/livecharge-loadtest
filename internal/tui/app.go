package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
	"livecharge/loadtest/internal/metrics"
)

// tickInterval is how often the model refreshes snapshots. 250 ms is the
// sweet spot between perceived liveness and CPU/redraw cost.
const tickInterval = 250 * time.Millisecond

// Model is the root Bubble Tea model. It owns the per-scenario state plus
// global UI state (active tab, selected scenario, window size).
//
// The scenario lists (runners / cleanups / snapshots / logBufs) are kept
// parallel: index N in all four refers to the same scenario. Mutations
// from 'a' (add) and 'x' (remove) keep them in sync via addScenario and
// removeScenarioAt.
type Model struct {
	runners   []*engine.Runner
	cleanups  []func()
	snapshots []metrics.Snapshot
	logBufs   []LogBuffer

	selected  int
	activeTab Tab

	width  int
	height int

	// quitting is set when the user pressed 'q' but the runners are still
	// draining. View() shows a banner during this window.
	quitting bool

	// logSink is an optional secondary destination for log lines (e.g. a
	// file opened by --log-file). nil means "TUI only".
	logSink io.Writer

	// Modals — at most one is open at a time. View() and handleKey()
	// route to whichever is active.
	manual  ManualModal
	picker  scenarioPicker
	confirm confirmDialog
	browser fileBrowser

	// candidates and builder enable the 'a' picker and 'b' file browser.
	// activePaths records which file paths are currently loaded so the
	// picker can filter them out. runnerPaths maps each runner pointer to
	// its file path so removeScenarioAt can update activePaths correctly.
	candidates  []ScenarioCandidate
	builder     ScenarioBuilder
	activePaths map[string]bool
	runnerPaths map[*engine.Runner]string

	// removeTarget is the slice index to remove when the confirm dialog is
	// accepted. Set by openRemoveConfirm; consumed in handleKey.
	removeTarget int

	// mailStatuses, when non-nil, sources per-scenario email status for
	// the Overview tab. Polled each tick alongside metric snapshots.
	mailStatuses MailStatusProvider
}

// New builds a Model from a Config.
func New(cfg Config) Model {
	runners := make([]*engine.Runner, 0, len(cfg.Initial))
	cleanups := make([]func(), 0, len(cfg.Initial))
	activePaths := map[string]bool{}
	runnerPaths := map[*engine.Runner]string{}
	for _, m := range cfg.Initial {
		runners = append(runners, m.Runner)
		cleanups = append(cleanups, m.OnRemove)
		if m.Path != "" {
			activePaths[m.Path] = true
			runnerPaths[m.Runner] = m.Path
		}
	}
	bufs := make([]LogBuffer, len(runners))
	snaps := make([]metrics.Snapshot, len(runners))
	for i, r := range runners {
		snaps[i] = r.Snapshot()
	}
	return Model{
		runners:      runners,
		cleanups:     cleanups,
		snapshots:    snaps,
		logBufs:      bufs,
		logSink:      cfg.LogSink,
		manual:       NewManualModal(cfg.Manual),
		browser:      newFileBrowser(cfg.BrowserRoots),
		candidates:   cfg.Candidates,
		builder:      cfg.Builder,
		activePaths:  activePaths,
		runnerPaths:  runnerPaths,
		mailStatuses: cfg.MailStatuses,
	}
}

// --- Bubble Tea messages ----------------------------------------------------

// tickMsg is emitted by the global ticker every tickInterval.
type tickMsg time.Time

// quitDoneMsg signals the main loop that all runners have drained and the
// program may exit.
type quitDoneMsg struct{}

// Init satisfies the Bubble Tea interface and kicks off the tick loop plus
// a goroutine per scenario that forwards log events into the program.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd()}
	for _, r := range m.runners {
		cmds = append(cmds, listenLog(r))
	}
	return tea.Batch(cmds...)
}

// tickCmd schedules the next tick.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// logEventMsg carries one log line from a runner to the model. We key it
// by the runner pointer (not slice index) so the message survives an
// add/remove that shifts the slice underneath us.
type logEventMsg struct {
	runner *engine.Runner
	line   string
}

// listenLog returns a Cmd that reads ONE line from the runner's log channel.
// Bubble Tea re-runs the command after each delivery, so we get a steady
// stream without blocking the model goroutine.
func listenLog(r *engine.Runner) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-r.LogCh()
		if !ok {
			return nil
		}
		return logEventMsg{runner: r, line: line}
	}
}

// Update is the central message handler.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		modalHeight := m.height - 3
		if modalHeight < 5 {
			modalHeight = 5
		}
		m.manual.Resize(m.width, modalHeight)
		m.browser.SetSize(m.width, modalHeight)
		return m, nil

	case tickMsg:
		for i, r := range m.runners {
			m.snapshots[i] = r.Snapshot()
		}
		if m.quitting && allTerminated(m.snapshots) {
			return m, func() tea.Msg { return quitDoneMsg{} }
		}
		return m, tickCmd()

	case logEventMsg:
		if idx := m.indexOf(msg.runner); idx >= 0 {
			scenarioName := m.runners[idx].Name()
			formatted := fmt.Sprintf("%s | %s", scenarioName, msg.line)
			m.logBufs[idx].Append(formatted)
			if m.logSink != nil {
				fmt.Fprintln(m.logSink, formatted)
			}
			// Keep listening on this runner's channel.
			return m, listenLog(msg.runner)
		}
		// Runner was removed; drop the message and the listener.
		return m, nil

	case quitDoneMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Forward unrecognised messages to the file browser when it's open
	// (it needs them for its internal directory-read tea.Cmd results).
	if m.browser.IsOpen() {
		cmd, picked := m.browser.Update(msg)
		if picked {
			m, addCmd := m.tryAddPath(m.browser.Selected(), &m.browser.status)
			return m, tea.Batch(cmd, addCmd)
		}
		return m, cmd
	}
	return m, nil
}

// handleKey routes key presses to whichever modal is open, else to the
// scenario actions. ctrl+c is the one universal escape that always quits.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// --- modal routing (priority: manual > picker > confirm > browser)
	if m.manual.IsOpen() && key != "ctrl+c" {
		_, cmd := m.manual.HandleKey(msg)
		return m, cmd
	}
	if m.confirm.IsOpen() && key != "ctrl+c" {
		_, confirmed := m.confirm.HandleKey(key)
		if confirmed {
			m.removeScenarioAt(m.removeTarget)
		}
		return m, nil
	}
	if m.picker.IsOpen() && key != "ctrl+c" {
		return m.handlePickerKey(key)
	}
	if m.browser.IsOpen() && key != "ctrl+c" {
		// Browser key handling lives in Update via fileBrowser.Update so
		// the embedded bubble sees the raw KeyMsg. We only intercept esc.
		if key == "esc" {
			m.browser.Close()
			return m, nil
		}
		cmd, picked := m.browser.Update(msg)
		if picked {
			m, addCmd := m.tryAddPath(m.browser.Selected(), &m.browser.status)
			return m, tea.Batch(cmd, addCmd)
		}
		return m, cmd
	}

	// --- empty list: only 'm' (manual), 'a' (add), 'b' (browse), 'q' (quit) make sense
	if len(m.runners) == 0 {
		switch key {
		case "m":
			m.manual.Open()
		case "a":
			m.openAddPicker()
		case "b":
			return m, m.openBrowser()
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		return m, nil
	}

	switch key {
	case "m":
		m.manual.Open()
		return m, nil
	case "a":
		m.openAddPicker()
		return m, nil
	case "b":
		return m, m.openBrowser()
	case "x":
		m.openRemoveConfirm()
		return m, nil
	case "ctrl+c", "q":
		m.quitting = true
		for _, r := range m.runners {
			if r.State() == engine.StateRunning {
				_ = r.Stop()
			}
		}
		return m, nil

	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(m.runners)-1 {
			m.selected++
		}

	case "1":
		m.activeTab = TabOverview
	case "2":
		m.activeTab = TabLatency
	case "3":
		m.activeTab = TabPredicates
	case "4":
		m.activeTab = TabLog

	case "s":
		r := m.runners[m.selected]
		if r.State() == engine.StateRunning {
			_ = r.Stop()
		}
	case "r":
		r := m.runners[m.selected]
		if r.State() == engine.StateStopped {
			_ = r.Resume()
		}
	case "R":
		_ = m.runners[m.selected].Restart()
	case " ":
		r := m.runners[m.selected]
		if r.State() == engine.StateIdle {
			_ = r.Start()
		}

	case "pgup":
		if m.activeTab == TabLog {
			m.logBufs[m.selected].ScrollUp(10)
		}
	case "pgdown", "pgdn":
		if m.activeTab == TabLog {
			m.logBufs[m.selected].ScrollDown(10)
		}
	}

	return m, nil
}

// handlePickerKey routes keys when the add-scenario picker is open.
func (m Model) handlePickerKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.picker.Close()
	case "up", "k":
		m.picker.Move(-1)
	case "down", "j":
		m.picker.Move(1)
	case "b":
		// Hand off to the file browser when the user wants to pick a
		// scenario that isn't in the auto-scanned directories. Closing
		// the picker first keeps modal precedence simple (only one
		// modal is open at a time).
		m.picker.Close()
		return m, m.openBrowser()
	case "enter":
		cand, ok := m.picker.Selected()
		if !ok {
			m.picker.Close()
			return m, nil
		}
		newModel, cmd := m.tryAddPath(cand.Path, nil)
		// tryAddPath sets picker.status when add fails; on success it
		// closes the picker for us.
		return newModel, cmd
	}
	return m, nil
}

// openAddPicker populates and shows the add-scenario picker, skipping
// candidates that are already in the active list.
func (m *Model) openAddPicker() {
	if m.builder == nil {
		return
	}
	available := make([]ScenarioCandidate, 0, len(m.candidates))
	for _, c := range m.candidates {
		if !m.activePaths[c.Path] {
			available = append(available, c)
		}
	}
	m.picker.Open("Add scenario", available)
}

// openBrowser opens the file browser modal and returns its initial Cmd
// (the bubble issues a directory read on first show).
func (m *Model) openBrowser() tea.Cmd {
	if m.builder == nil {
		return nil
	}
	return m.browser.Open()
}

// openRemoveConfirm prompts the user before removing the selected runner.
// The index is stored in m.removeTarget; the actual removal happens in
// handleKey when the user presses 'y', keeping the mutation on the live model.
func (m *Model) openRemoveConfirm() {
	if m.selected < 0 || m.selected >= len(m.runners) {
		return
	}
	name := m.runners[m.selected].Name()
	m.removeTarget = m.selected
	m.confirm.Open(fmt.Sprintf("Remove scenario '%s' from the suite?", name), nil)
}

// tryAddPath loads + builds + starts the scenario at path. On success it
// closes any open picker/browser. On failure it writes the error to
// pickerStatus (or browser.status if pickerStatus is nil) and keeps the
// modal open so the user can pick something else.
func (m Model) tryAddPath(path string, pickerStatus *string) (Model, tea.Cmd) {
	if m.builder == nil || path == "" {
		return m, nil
	}
	ms, err := m.builder(path)
	if err != nil {
		msg := "add failed: " + err.Error()
		if pickerStatus != nil {
			*pickerStatus = msg
		} else {
			m.picker.SetStatus(msg)
		}
		return m, nil
	}
	cmd := m.addScenario(ms, path)
	m.picker.Close()
	m.browser.Close()
	return m, cmd
}

// addScenario appends a Managed entry to all four parallel slices and
// returns the listener Cmd for its log channel.
func (m *Model) addScenario(ms ManagedScenario, path string) tea.Cmd {
	m.runners = append(m.runners, ms.Runner)
	m.cleanups = append(m.cleanups, ms.OnRemove)
	m.snapshots = append(m.snapshots, ms.Runner.Snapshot())
	m.logBufs = append(m.logBufs, LogBuffer{})
	if path != "" {
		m.activePaths[path] = true
		m.runnerPaths[ms.Runner] = path
	}
	// Select the new one so it's immediately visible.
	m.selected = len(m.runners) - 1
	return listenLog(ms.Runner)
}

// removeScenarioAt tears down the scenario at idx and removes it from
// all parallel slices. cleanup may take a moment (Stop drains in-flight
// sessions); we accept that pause because removal is a user-initiated
// action and a clean drain matters more than instant UI response.
func (m *Model) removeScenarioAt(idx int) {
	if idx < 0 || idx >= len(m.runners) {
		return
	}
	// Remove from activePaths so the 'a' picker can offer this scenario
	// again. runnerPaths is keyed by pointer so this works for both
	// initially-loaded scenarios and ones added at runtime.
	runner := m.runners[idx]
	if path, ok := m.runnerPaths[runner]; ok {
		delete(m.activePaths, path)
		delete(m.runnerPaths, runner)
	}
	if cleanup := m.cleanups[idx]; cleanup != nil {
		cleanup()
	}
	m.runners = append(m.runners[:idx], m.runners[idx+1:]...)
	m.cleanups = append(m.cleanups[:idx], m.cleanups[idx+1:]...)
	m.snapshots = append(m.snapshots[:idx], m.snapshots[idx+1:]...)
	m.logBufs = append(m.logBufs[:idx], m.logBufs[idx+1:]...)
	if m.selected >= len(m.runners) {
		m.selected = len(m.runners) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// indexOf returns the slice index of r, or -1 if r is no longer active.
func (m *Model) indexOf(r *engine.Runner) int {
	for i, x := range m.runners {
		if x == r {
			return i
		}
	}
	return -1
}

// View composes the final frame. Sidebar on the left, detail on the right,
// footer with key hints at the bottom.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}

	contentHeight := m.height - 3
	if contentHeight < 5 {
		contentHeight = 5
	}

	header := RenderHeader(m.width)

	// Modal overlay precedence: manual > picker > confirm > browser.
	if m.manual.IsOpen() {
		body := m.manual.View(m.width, contentHeight)
		footer := RenderFooter(m.width, fmt.Sprintf(
			"[↑↓/jk] scroll  [PgUp/PgDn] page  [m/esc] close manual   %3.0f%%",
			m.manual.ScrollPercent()*100,
		))
		return strings.Join([]string{header, body, footer}, "\n")
	}
	if m.picker.IsOpen() {
		body := m.picker.View(m.width, contentHeight)
		footer := RenderFooter(m.width, "[↑↓] move  [enter] add  [b] browse files  [esc] cancel")
		return strings.Join([]string{header, body, footer}, "\n")
	}
	if m.confirm.IsOpen() {
		body := m.confirm.View(m.width, contentHeight)
		footer := RenderFooter(m.width, "[y/enter] yes   [n/esc] cancel")
		return strings.Join([]string{header, body, footer}, "\n")
	}
	if m.browser.IsOpen() {
		body := m.browser.View(m.width, contentHeight)
		footer := RenderFooter(m.width, "[↑↓] move  [enter] open/select  [esc] cancel")
		return strings.Join([]string{header, body, footer}, "\n")
	}

	sidebar := renderSidebar(m.snapshots, m.selected, contentHeight)

	detailWidth := m.width - lipgloss.Width(sidebar) - 2
	if detailWidth < 20 {
		detailWidth = 20
	}

	var snap metrics.Snapshot
	var logBuf *LogBuffer
	var runner *engine.Runner
	var mailStatus *mail.Status
	if m.selected < len(m.snapshots) {
		snap = m.snapshots[m.selected]
		logBuf = &m.logBufs[m.selected]
		runner = m.runners[m.selected]
		if m.mailStatuses != nil {
			mailStatus = m.mailStatuses.StatusFor(runner)
		}
	}
	detail := StyleDetail.Copy().Width(detailWidth).Height(contentHeight).Render(
		renderDetail(m.activeTab, snap, runner, logBuf, mailStatus, detailWidth-2, contentHeight),
	)

	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, detail)

	hints := []string{
		"[1-4] tab",
		"[↑↓] scenario",
		"[Space] start",
		"[s] stop",
		"[r] resume",
		"[R] restart",
		"[a] add",
		"[x] remove",
		"[b] browse",
		"[m] manual",
		"[q] quit",
	}
	footerText := strings.Join(hints, "  ")
	if m.quitting {
		footerText = "Stopping all scenarios… " + footerText
	}
	footer := RenderFooter(m.width, footerText)

	return strings.Join([]string{header, main, footer}, "\n")
}

// allTerminated returns true when every scenario is in a terminal state
// (DONE, ERROR, or STOPPED) — used during quit to decide when to exit.
func allTerminated(snaps []metrics.Snapshot) bool {
	for _, s := range snaps {
		if s.StateName == "RUNNING" {
			return false
		}
	}
	return true
}

// Run is a convenience that wraps tea.NewProgram with sensible defaults.
// AltScreen so the terminal restores its previous contents on exit.
func Run(cfg Config) error {
	p := tea.NewProgram(New(cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
