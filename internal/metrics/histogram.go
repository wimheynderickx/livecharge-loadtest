package metrics

import (
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

// LatencyHistogram wraps hdrhistogram.Histogram with a mutex so it can be
// updated by the single collector goroutine and queried concurrently by
// snapshot readers.
//
// The histogram records latencies in **microseconds**. We chose microseconds
// because sub-millisecond observations are common when load-testing local
// mocks; recording in ms would round everything below 1 ms to a single bar
// and make decimal-ms bucket edges (e.g. 0.1, 0.5) meaningless. Three
// significant digits of HDR precision is still more than enough — 12 µs is
// distinguishable from 12.1 µs.
type LatencyHistogram struct {
	mu sync.Mutex
	h  *hdrhistogram.Histogram
}

// NewLatencyHistogram builds a histogram with [1, maxUs] range and 3
// significant digits of precision. Callers should pass the scenario's
// response_timeout converted to microseconds; we clamp recordings to that
// upper bound so timeouts land in the last bucket instead of being dropped.
func NewLatencyHistogram(maxUs int64) *LatencyHistogram {
	if maxUs < 1 {
		maxUs = 1
	}
	return &LatencyHistogram{
		h: hdrhistogram.New(1, maxUs, 3),
	}
}

// Record stores one latency observation. Out-of-range values are clipped
// to the histogram's upper bound to avoid losing data on the slow tail.
func (lh *LatencyHistogram) Record(d time.Duration) {
	us := d.Microseconds()
	if us < 1 {
		us = 1
	}
	lh.mu.Lock()
	// RecordValue returns an error when out of range; we clamp instead so
	// timeouts (which sit right at the upper bound) still get counted.
	if err := lh.h.RecordValue(us); err != nil {
		_ = lh.h.RecordValue(lh.h.HighestTrackableValue())
	}
	lh.mu.Unlock()
}

// Percentile returns the latency at the given percentile (0..100).
func (lh *LatencyHistogram) Percentile(p float64) time.Duration {
	lh.mu.Lock()
	v := lh.h.ValueAtQuantile(p)
	lh.mu.Unlock()
	return time.Duration(v) * time.Microsecond
}

// Count returns the total number of recorded observations.
func (lh *LatencyHistogram) Count() int64 {
	lh.mu.Lock()
	c := lh.h.TotalCount()
	lh.mu.Unlock()
	return c
}

// Reset zeroes all counts. Used by Restart().
func (lh *LatencyHistogram) Reset() {
	lh.mu.Lock()
	lh.h.Reset()
	lh.mu.Unlock()
}

// PercentileMap returns the latency at every requested percentile. Called
// by Collector.Snapshot.
func (lh *LatencyHistogram) PercentileMap(percentiles []float64) map[float64]time.Duration {
	out := make(map[float64]time.Duration, len(percentiles))
	lh.mu.Lock()
	for _, p := range percentiles {
		us := lh.h.ValueAtQuantile(p)
		out[p] = time.Duration(us) * time.Microsecond
	}
	lh.mu.Unlock()
	return out
}

// Buckets returns a CDF-friendly bucketed view: counts in each of the
// supplied edges (in microseconds). The returned slice has len(edges)+1
// entries; the last one collects everything above the highest edge.
//
// Implemented on top of Distribution(), which returns the histogram's
// own variable-width bars; we re-aggregate them into the caller's edges.
// Used by the TUI Latency tab.
func (lh *LatencyHistogram) Buckets(bucketEdgesUs []int64) []int64 {
	out := make([]int64, len(bucketEdgesUs)+1)
	lh.mu.Lock()
	bars := lh.h.Distribution()
	lh.mu.Unlock()

	for _, bar := range bars {
		if bar.Count == 0 {
			continue
		}
		v := bar.To // upper edge of the hdr bar, in microseconds
		placed := false
		for i, edge := range bucketEdgesUs {
			if v <= edge {
				out[i] += bar.Count
				placed = true
				break
			}
		}
		if !placed {
			out[len(bucketEdgesUs)] += bar.Count
		}
	}
	return out
}
