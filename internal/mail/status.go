package mail

import (
	"fmt"
	"sync"
	"time"
)

// State is one of the four lifecycle states for an outbound email.
type State int

const (
	// StateDisabled means the email feature is off for this scenario.
	// The TUI hides the email row entirely.
	StateDisabled State = iota
	// StateConfigured — email is on and validated, but no send has fired
	// yet. The TUI shows the configured triggers + interval so the user
	// can see what's wired up before any lifecycle event happens.
	StateConfigured
	// StatePending — goroutine spawned, dialogue not yet finished.
	StatePending
	// StateSent — server accepted the message.
	StateSent
	// StateFailed — dialogue, template render, or attachment build failed.
	// Err is populated.
	StateFailed
)

// String returns a human label for logging.
func (s State) String() string {
	switch s {
	case StateDisabled:
		return "disabled"
	case StateConfigured:
		return "configured"
	case StatePending:
		return "pending"
	case StateSent:
		return "sent"
	case StateFailed:
		return "failed"
	}
	return "unknown"
}

// Status is the thread-safe holder the TUI polls and the sender goroutine
// writes into. Zero value is usable: starts as StateDisabled.
//
// The mutex covers all reads and writes because the TUI snapshot runs on
// the bubbletea goroutine while the sender goroutine may finish at any
// time. Reads are infrequent (once per tick) and writes happen exactly
// twice per send so the lock contention is negligible.
type Status struct {
	mu       sync.Mutex
	state    State
	err      error
	startAt  time.Time
	finishAt time.Time
	// recipient is shown in the TUI so the user can see where the mail
	// went without expanding details. Empty when the email never started.
	recipient string
	// trigger names the lifecycle event that fired the email — "start",
	// "progress", "done", or "error". Shown alongside the state in the
	// TUI so the user always knows which event the displayed status
	// belongs to (since several can occur for the same scenario).
	trigger string
	// triggers and reportInterval capture the resolved email config so
	// the TUI can show "what's wired up" alongside the latest send
	// state. Populated once by MarkConfigured at wiring time and stable
	// for the run.
	triggers       []string
	reportInterval time.Duration
}

// State returns the current state.
func (s *Status) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Snapshot returns a read-consistent view of the entire status. Caller may
// retain the result indefinitely without affecting future writes.
type Snapshot struct {
	State          State
	Err            error
	StartAt        time.Time
	FinishAt       time.Time
	Recipient      string
	Trigger        string
	Triggers       []string
	ReportInterval time.Duration
}

// Snapshot returns the current values as a frozen struct.
func (s *Status) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	var triggers []string
	if len(s.triggers) > 0 {
		triggers = append([]string(nil), s.triggers...)
	}
	return Snapshot{
		State:          s.state,
		Err:            s.err,
		StartAt:        s.startAt,
		FinishAt:       s.finishAt,
		Recipient:      s.recipient,
		Trigger:        s.trigger,
		Triggers:       triggers,
		ReportInterval: s.reportInterval,
	}
}

// SetTrigger records which lifecycle event the most recent send was for.
// Called by the email subsystem right before MarkPending so the TUI sees
// the correct trigger associated with the new state transition.
func (s *Status) SetTrigger(trigger string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trigger = trigger
}

// MarkPending is called from the sender goroutine immediately after it
// starts. The recipient hint is purely cosmetic — it shows up in the TUI.
func (s *Status) MarkPending(recipient string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StatePending
	s.recipient = recipient
	s.startAt = time.Now()
}

// MarkSent transitions to Sent and records the wall-clock finish time.
func (s *Status) MarkSent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateSent
	s.finishAt = time.Now()
}

// MarkFailed transitions to Failed and records the underlying error.
func (s *Status) MarkFailed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateFailed
	s.err = err
	s.finishAt = time.Now()
}

// MarkDisabled is called when the feature is off for this scenario so the
// TUI can distinguish "intentionally off" from "we'll send shortly".
func (s *Status) MarkDisabled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateDisabled
}

// MarkConfigured records the resolved trigger list and progress cadence
// and moves the status out of Disabled. Called once at wiring time so
// the TUI can show "email is on; here's what fires" before any send.
// The reportInterval is only meaningful when "progress" is in triggers;
// callers may pass zero otherwise.
func (s *Status) MarkConfigured(triggers []string, reportInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateConfigured
	s.triggers = append(s.triggers[:0], triggers...)
	s.reportInterval = reportInterval
}

// Summary returns a one-line description suitable for the TUI footer.
// Includes the trigger reason in parentheses when known so the user can
// tell a "progress" email apart from a "done" email at a glance.
func (s *Status) Summary() string {
	snap := s.Snapshot()
	suffix := ""
	if snap.Trigger != "" {
		suffix = " (" + snap.Trigger + ")"
	}
	switch snap.State {
	case StateDisabled:
		return ""
	case StateConfigured:
		return "configured" + suffix
	case StatePending:
		return fmt.Sprintf("sending%s to %s…", suffix, snap.Recipient)
	case StateSent:
		return fmt.Sprintf("sent%s to %s at %s",
			suffix, snap.Recipient, snap.FinishAt.Format("15:04:05"))
	case StateFailed:
		return fmt.Sprintf("FAILED%s — %v", suffix, snap.Err)
	}
	return ""
}
