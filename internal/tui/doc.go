// Package tui implements the live terminal dashboard for loadtest.
//
// The UI is split into two panes:
//
//   - A left sidebar that lists every running scenario, its state badge,
//     and the most important counters.
//   - A right detail panel with four tabs (Overview, Latency, Predicates, Log)
//     for the currently selected scenario.
//
// Built on Bubble Tea (Elm-style architecture: Model -> Update(msg) -> View()).
// A 250 ms ticker drives metric polling; nothing in this package talks to the
// engine directly — it only consumes metrics.Snapshot values and Collector
// log lines, then issues lifecycle commands (Start/Stop/Resume/Restart)
// through the engine.ScenarioRunner interface.
package tui
