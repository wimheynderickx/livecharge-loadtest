package tui

import (
	"io"

	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
)

// MailStatusProvider exposes the post-run email status for a given runner.
// The CLI wires the registry in cmd/loadtest/email.go; the TUI keeps a
// reference and polls each tick to refresh the Overview tab.
//
// Implementations must return a non-nil *mail.Status (use the Disabled
// state when email is off for that scenario). Returning nil makes the
// Overview tab hide the email row.
type MailStatusProvider interface {
	StatusFor(*engine.Runner) *mail.Status
}

// ManagedScenario bundles a running engine.Runner with the teardown
// callback that releases everything it owns (CSV writer, transport, …).
//
// The dashboard calls OnRemove when:
//   - the user removes a scenario via the 'x' key, or
//   - the program exits gracefully
//
// OnRemove must Stop the runner (drains in-flight sessions) and stop any
// satellite resources (CSV writers, file handles). It is provided by the
// caller — the TUI deliberately doesn't know about config or report.
type ManagedScenario struct {
	Runner   *engine.Runner
	OnRemove func()
	// Path is the absolute path of the TOML file this scenario was loaded
	// from. The dashboard uses it to track which candidates are active so
	// removed scenarios can reappear in the 'a' picker.
	Path string
}

// ScenarioCandidate is a TOML file the user can add to the dashboard via
// the 'a' picker. Path is opaque to the TUI; it's the identifier passed
// back to ScenarioBuilder.
type ScenarioCandidate struct {
	Name        string
	Description string
	Path        string
}

// ScenarioBuilder loads the TOML at path, builds a fresh runner, starts
// any CSV writer the file requests, calls Start() on the runner, and
// returns the resulting ManagedScenario. The caller (cmd/loadtest/run.go)
// implements this — the TUI keeps no dependency on config or report.
type ScenarioBuilder func(path string) (ManagedScenario, error)

// Config is the parameter bundle for tui.New / tui.Run.
//
// All fields are optional except Initial. Pass a non-empty Candidates +
// Builder to enable the 'a' add-scenario picker. Pass any Path roots in
// BrowserRoots to enable the 'b' file browser; an empty slice falls back
// to the current working directory.
type Config struct {
	// Initial is the set of scenarios already started by the caller and
	// displayed when the dashboard opens.
	Initial []ManagedScenario

	// LogSink, when non-nil, receives every log line as text (used by
	// --log-file). The dashboard's Log tab keeps its own ring buffer
	// regardless.
	LogSink io.Writer

	// Manual is the pre-rendered operational manual shown by the 'm'
	// shortcut. Empty disables the modal.
	Manual string

	// Candidates is the list of scenarios the 'a' picker offers.
	// Typically populated by scanning --config-dirs at startup.
	Candidates []ScenarioCandidate

	// Builder constructs a fresh runner from a candidate's Path. Required
	// when either Candidates or the file browser is in use.
	Builder ScenarioBuilder

	// BrowserRoots are the directories where the 'b' file browser starts.
	// The first entry is used as the initial location; the others appear
	// as quick-jump shortcuts (planned). Empty defaults to ".".
	BrowserRoots []string

	// MailStatuses, when non-nil, lets the Overview tab show post-run
	// email status (sending / sent / failed) per scenario. nil disables
	// the email row entirely.
	MailStatuses MailStatusProvider
}
