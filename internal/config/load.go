package config

// LoadConfig is the [load] block: how much traffic to generate and how
// long to run.
type LoadConfig struct {
	// Rate is the target throughput in messages per second.
	// A value of 0 means "as fast as possible" (no rate limiting).
	Rate float64 `toml:"rate"`

	// TotalMessages caps the number of messages sent for the whole scenario.
	// 0 means "no cap"; the scenario then stops when Duration elapses or
	// when the user cancels.
	TotalMessages int64 `toml:"total_messages"`

	// Duration caps how long the scenario runs.
	// 0 means "no cap"; the scenario runs until TotalMessages is reached
	// or the user cancels.
	Duration Duration `toml:"duration"`

	// Concurrency is the number of virtual users (goroutines) running
	// in parallel. Each goroutine drives one session at a time.
	Concurrency int `toml:"concurrency"`

	// ResponseTimeout is how long to wait for a reply before recording a
	// timeout error. It also serves as the upper bound for the latency
	// histogram. Default: 2 seconds (applied during validation).
	ResponseTimeout Duration `toml:"response_timeout"`
}

// MetricsConfig is the [metrics] block.
type MetricsConfig struct {
	// Percentiles is the list of percentiles to report (e.g. [50, 95, 99]).
	// Each value here becomes a column in the CSV report and a row on the
	// TUI Overview tab. Default (applied during validation): [50, 75, 90, 95, 99, 99.9].
	Percentiles []float64 `toml:"percentiles"`

	// BucketCount controls how many bars the TUI Latency tab shows.
	// Default 20. The range [0, response_timeout] is split into BucketCount
	// buckets; 75% of them cover the first 50% of the range (fine
	// resolution where latencies typically cluster), and the remaining 25%
	// cover the slower tail. Ignored when BucketEdgesMs is set.
	BucketCount int `toml:"bucket_count"`

	// BucketEdgesMs is a fully manual override: each entry is the upper
	// bound of one bucket (inclusive), in milliseconds. Floating-point
	// values are accepted so sub-millisecond edges like 0.1 or 0.5 are
	// meaningful (the histogram records latencies at microsecond
	// resolution internally). Values must be strictly increasing. When
	// this list is non-empty, BucketCount is ignored. The TUI shows
	// len(BucketEdgesMs) bars plus one "above max" overflow bar.
	BucketEdgesMs []float64 `toml:"bucket_edges_ms"`
}

// ReportConfig is the [report] block. It is optional; absent means no CSV is written.
type ReportConfig struct {
	// CSVPath is the destination file for periodic CSV snapshots.
	// The substring "{timestamp}" is replaced at scenario start with
	// time.Now().Format(TimestampFormat).
	CSVPath string `toml:"csv_path"`

	// TimestampFormat is the Go time-layout string used to render
	// "{timestamp}" placeholders. Default: "2006-01-02T15-04-05".
	TimestampFormat string `toml:"timestamp_format"`

	// Overwrite controls how the file is opened.
	//   true  — truncate the file on open (default)
	//   false — append to an existing file
	Overwrite *bool `toml:"overwrite"`

	// FlushInterval is how often to write a row to the CSV.
	// Default: 10 seconds.
	FlushInterval Duration `toml:"flush_interval"`
}
