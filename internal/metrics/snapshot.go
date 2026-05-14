package metrics

import "time"

// Snapshot is a read-consistent view of one scenario's metrics at a point
// in time. The collector goroutine produces it; callers may retain it
// indefinitely without affecting the collector.
type Snapshot struct {
	// ScenarioName and ScenarioDescription are copied from the scenario
	// config so the TUI can display them without importing the config package.
	ScenarioName        string
	ScenarioDescription string

	// State is the scenario's current lifecycle state, encoded as a string
	// to avoid importing engine.State here (which would create a cycle:
	// engine → metrics → engine).
	StateName string

	// ElapsedMs is the live elapsed time including resumed runs.
	ElapsedMs int64

	// TotalTarget is [load] total_messages; 0 means unlimited.
	TotalTarget int64

	// Counters
	Sent     int64
	Received int64
	Errors   int64

	// MsgPerSec is the current 1-second sliding-window throughput.
	MsgPerSec float64

	// MaxMsgPerSec is the peak value MsgPerSec has reached since the
	// scenario was started (or last Restart()). Sampled every time
	// Snapshot() runs, so the resolution matches the caller's polling
	// frequency (250 ms for the TUI).
	MaxMsgPerSec float64

	// AvgMsgPerSec is Sent divided by elapsed seconds — a stable lifetime
	// average that ignores burstiness. 0 until the first ms of elapsed
	// time has passed.
	AvgMsgPerSec float64

	// Percentiles maps each configured percentile (50, 95, 99, …) to its
	// latency.
	Percentiles map[float64]time.Duration

	// Predicates maps each named predicate to its counters and per-name
	// percentiles.
	Predicates map[string]PredicateStats
}

// PredicateStats holds the per-predicate accounting.
type PredicateStats struct {
	Count       int64
	Percentiles map[float64]time.Duration
}
