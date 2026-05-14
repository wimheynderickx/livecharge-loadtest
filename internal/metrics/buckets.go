package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// fineRatioBuckets controls how the automatic bucket layout splits the range.
// 0.75 means "75% of the buckets cover the first 50% of the range". This is
// where realistic latencies cluster, so most of the visual resolution lives
// there; the remaining 25% of buckets cover the slower tail (and timeouts).
//
// fineRangeBuckets is the corresponding range share (0.5 = first half).
const (
	fineRatioBuckets = 0.75
	fineRangeBuckets = 0.5
)

// ResolveBuckets returns the edge list and matching human-readable labels
// for the latency histogram.
//
// Units:
//
//   - manualEdgesMs is the user-facing config: each entry is the upper
//     bound of one bucket in milliseconds, with full float resolution
//     (0.1, 0.5, 1.0, …).
//   - maxUs is the histogram's upper bound in microseconds (typically
//     response_timeout converted to µs).
//   - The returned edges are in microseconds (int64) because the histogram
//     itself records µs and Buckets() needs matching units.
//
// Layouts:
//
//   - manualEdgesMs non-empty: each float ms is multiplied by 1000 and
//     rounded to the nearest int64 µs.
//   - manualEdgesMs empty: [0, maxUs] is split into count buckets;
//     75% of the buckets cover the first 50% of the range.
//
// Labels match the edges (one per bar plus a trailing overflow label).
// Labels are formatted in ms with the minimum number of decimals needed
// (so "0.1 ms" or "10 ms" instead of "100 µs" or "10.000 ms").
func ResolveBuckets(manualEdgesMs []float64, count int, maxUs int64) (edges []int64, labels []string) {
	if len(manualEdgesMs) > 0 {
		edges = manualEdgesToUs(manualEdgesMs)
	} else {
		edges = autoBucketEdgesUs(count, maxUs)
	}
	labels = bucketLabels(edges)
	return edges, labels
}

// manualEdgesToUs converts ms floats to µs ints, snapping every entry to
// at least 1 µs above its predecessor so we never emit duplicate edges
// (which would break Buckets() and confuse the bar chart).
func manualEdgesToUs(edgesMs []float64) []int64 {
	out := make([]int64, 0, len(edgesMs))
	for _, ms := range edgesMs {
		us := int64(ms*1000 + 0.5)
		if us < 1 {
			us = 1
		}
		out = appendStrictlyIncreasing(out, us)
	}
	return out
}

// autoBucketEdgesUs builds the proportional 75% fine / 25% coarse layout
// across the [0, maxUs] range. All edges are integer µs.
func autoBucketEdgesUs(count int, maxUs int64) []int64 {
	if count < 2 {
		count = 20
	}
	if maxUs < 2 {
		maxUs = 2
	}

	fineParts := int(float64(count)*fineRatioBuckets + 0.5)
	if fineParts < 1 {
		fineParts = 1
	}
	if fineParts >= count {
		fineParts = count - 1
	}
	coarseParts := count - fineParts

	fineMax := int64(float64(maxUs) * fineRangeBuckets)
	if fineMax < int64(fineParts) {
		// With a tiny maxUs and many fine parts we can't give each bucket
		// a unique integer upper bound. Force the smallest valid layout.
		fineMax = int64(fineParts)
		if fineMax > maxUs {
			fineMax = maxUs
		}
	}

	edges := make([]int64, 0, count)
	for i := 1; i <= fineParts; i++ {
		edges = appendStrictlyIncreasing(edges, fineMax*int64(i)/int64(fineParts))
	}
	for i := 1; i <= coarseParts; i++ {
		edges = appendStrictlyIncreasing(edges, fineMax+(maxUs-fineMax)*int64(i)/int64(coarseParts))
	}
	return edges
}

// appendStrictlyIncreasing adds e to edges only when it sits above every
// existing entry. Rounding can collapse adjacent edges to the same int when
// the range is small; we'd rather bump the duplicate by one than emit it
// and break label/bucket alignment downstream.
func appendStrictlyIncreasing(edges []int64, e int64) []int64 {
	if len(edges) > 0 && e <= edges[len(edges)-1] {
		e = edges[len(edges)-1] + 1
	}
	return append(edges, e)
}

// bucketLabels turns a list of µs edges into N+1 right-padded labels for
// the TUI bar chart. Each label renders the bracket in milliseconds with
// just enough decimals to be unambiguous.
func bucketLabels(edgesUs []int64) []string {
	out := make([]string, 0, len(edgesUs)+1)

	var prevUs int64 = 0
	for _, e := range edgesUs {
		out = append(out, fmt.Sprintf("%s–%s ms", formatEdgeMs(prevUs), formatEdgeMs(e)))
		prevUs = e
	}
	out = append(out, fmt.Sprintf(">%s ms", formatEdgeMs(prevUs)))

	// Pad to the widest label so the bars line up cleanly.
	max := 0
	for _, s := range out {
		if n := visualWidthRunes(s); n > max {
			max = n
		}
	}
	for i, s := range out {
		out[i] = s + strings.Repeat(" ", max-visualWidthRunes(s))
	}
	return out
}

// formatEdgeMs renders a µs value as a short ms string, dropping trailing
// zeros. 100 µs → "0.1", 1000 µs → "1", 12345 µs → "12.345".
func formatEdgeMs(us int64) string {
	if us == 0 {
		return "0"
	}
	return strconv.FormatFloat(float64(us)/1000.0, 'f', -1, 64)
}

// visualWidthRunes counts runes (not bytes). The en-dash takes 3 bytes in
// UTF-8 but renders as one column, so we count runes for alignment.
func visualWidthRunes(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// FormatLatency renders a time.Duration as a milliseconds string with an
// adaptive number of decimals. Used by the TUI, CSV, and headless summary
// so all latency output stays consistent.
//
// Examples:
//
//	250 µs   → "0.250 ms"
//	1.234 ms → "1.23 ms"
//	12 ms    → "12 ms"
//	1.5 s    → "1500 ms"
func FormatLatency(d time.Duration) string {
	us := d.Microseconds()
	switch {
	case us < 1000:
		return fmt.Sprintf("%.3f ms", float64(us)/1000.0)
	case us < 10_000:
		return fmt.Sprintf("%.2f ms", float64(us)/1000.0)
	case us < 100_000:
		return fmt.Sprintf("%.1f ms", float64(us)/1000.0)
	default:
		return fmt.Sprintf("%d ms", us/1000)
	}
}

// LatencyMs returns the duration as milliseconds with full µs precision,
// suitable for CSV columns where downstream parsers need a stable
// fractional number rather than a human-formatted string.
func LatencyMs(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}
