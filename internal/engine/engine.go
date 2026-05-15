package engine

import (
	"livecharge/loadtest/internal/metrics"
)

// State is the lifecycle position of a ScenarioRunner.
type State int

const (
	StateIdle State = iota
	StateRunning
	StateStopped
	StateDone
	StateError
	StateScriptError // configuration is valid TOML but an expr predicate failed to compile; the runner cannot start.
)

// String returns the human-readable state name. Snapshot.StateName uses
// these so the TUI and CSV writer never need to import this package just
// to format a status word.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateRunning:
		return "RUNNING"
	case StateStopped:
		return "STOPPED"
	case StateDone:
		return "DONE"
	case StateError:
		return "ERROR"
	case StateScriptError:
		return "SCRIPT_ERROR"
	default:
		return "UNKNOWN"
	}
}

// ScenarioRunner is the contract for one running scenario.
//
// Each call corresponds to a user action in the TUI or a step in the
// headless lifecycle. See docs/superpowers/specs/2026-05-13-... §5.4 for
// the state-transition rules.
type ScenarioRunner interface {
	// Start begins generating load. Must be called once before Stop/Resume.
	Start() error
	// Stop drains in-flight sessions, banks elapsed time and message count,
	// and parks the runner in STOPPED.
	Stop() error
	// Resume continues from a STOPPED state.
	Resume() error
	// Restart resets counters and metrics, then Starts.
	Restart() error
	// State returns the current lifecycle position.
	State() State
	// Snapshot returns a read-consistent metrics view.
	Snapshot() metrics.Snapshot
	// Name returns the scenario name (for logs and the TUI sidebar).
	Name() string
}
