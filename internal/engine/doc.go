// Package engine orchestrates the actual load generation.
//
// Components:
//
//   - State          — the lifecycle states (IDLE, RUNNING, STOPPED, DONE, ERROR).
//   - ScenarioRunner — the public interface: Start/Stop/Resume/Restart.
//   - session        — one virtual user; runs the step loop until the
//     session ends (predicate "" or end of steps).
//   - LoadGenerator  — token-bucket rate limiter + goroutine pool. Each
//     goroutine drives one session at a time and immediately starts a new
//     one when the previous session ends.
//   - PreCalc        — at startup, detects steps whose templates can be
//     rendered once and reused; saves CPU at high rates.
//
// The engine deliberately does not know anything about TUIs or CSV files.
// Observers read state through metrics.Snapshot.
package engine
