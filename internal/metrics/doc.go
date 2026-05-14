// Package metrics collects per-scenario performance numbers and exposes
// read-consistent snapshots.
//
// Design rules:
//
//   - The MetricsCollector is the only writer. Sessions submit StepResult
//     events through a channel; a single goroutine drains the channel and
//     updates counters and histograms.
//
//   - All readers (TUI, CSV reporter, headless summary) call Snapshot. The
//     Snapshot returns plain copies — no shared pointers — so callers can
//     safely retain them.
//
// Single-writer means there is no contention between the engine (which
// generates events at hundreds of msg/sec) and readers (which sample every
// 250 ms). Histogram writes use a mutex internally because hdrhistogram
// itself isn't safe for concurrent updates.
package metrics
