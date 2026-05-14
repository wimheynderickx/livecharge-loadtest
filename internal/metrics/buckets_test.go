package metrics

import (
	"testing"
	"time"
)

// All max values below are in microseconds — the histogram's native unit.
// 2000 ms response_timeout = 2_000_000 µs.
const twoSecondsUs = int64(2_000_000)

// TestResolveBuckets_AutoDefault verifies the documented rule:
// 20 buckets with the first 15 covering the first 50% of [0, maxUs].
func TestResolveBuckets_AutoDefault(t *testing.T) {
	edges, labels := ResolveBuckets(nil, 20, twoSecondsUs)
	if got := len(edges); got != 20 {
		t.Fatalf("want 20 edges, got %d", got)
	}
	if got := len(labels); got != 21 {
		t.Fatalf("want 21 labels (20 buckets + overflow), got %d", got)
	}
	// Edge 14 (0-indexed) is the 15th fine bucket — it sits at the
	// halfway point of the range. 50% of 2_000_000 µs = 1_000_000 µs.
	if edges[14] != 1_000_000 {
		t.Errorf("want edges[14] = 1_000_000 µs (1000 ms boundary), got %d", edges[14])
	}
	if edges[19] != twoSecondsUs {
		t.Errorf("want last edge = %d µs, got %d", twoSecondsUs, edges[19])
	}
}

// TestResolveBuckets_AutoProportional verifies that the 75% fine ratio scales
// with bucket_count: 40 buckets → first 30 cover the lower half.
func TestResolveBuckets_AutoProportional(t *testing.T) {
	edges, _ := ResolveBuckets(nil, 40, twoSecondsUs)
	if got := len(edges); got != 40 {
		t.Fatalf("want 40 edges, got %d", got)
	}
	// 40 * 0.75 = 30 fine buckets; edges[29] (0-indexed) is the 30th.
	if edges[29] != 1_000_000 {
		t.Errorf("want edges[29] = 1_000_000 µs (1000 ms boundary), got %d", edges[29])
	}
}

// TestResolveBuckets_Manual verifies that an explicit edge list (in
// float ms) round-trips through Resolve into matching µs edges.
func TestResolveBuckets_Manual(t *testing.T) {
	manualMs := []float64{0.1, 0.5, 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2000}
	wantUs := []int64{100, 500, 1000, 5000, 10000, 25000, 50000, 100000, 250000, 500000, 1000000, 2000000}

	edges, labels := ResolveBuckets(manualMs, 0, twoSecondsUs)
	if len(edges) != len(manualMs) {
		t.Fatalf("want %d edges, got %d", len(manualMs), len(edges))
	}
	for i, want := range wantUs {
		if edges[i] != want {
			t.Errorf("edge[%d]: want %d µs, got %d µs", i, want, edges[i])
		}
	}
	if got := len(labels); got != len(manualMs)+1 {
		t.Errorf("want %d labels, got %d", len(manualMs)+1, got)
	}
	// First bar should read "0–0.1 ms" (trimmed of trailing padding).
	if got := trim(labels[0]); got != "0–0.1 ms" {
		t.Errorf("want first label = %q, got %q", "0–0.1 ms", got)
	}
}

// TestResolveBuckets_StrictlyIncreasing makes sure the auto layout never
// produces collapsed adjacent edges even at small ranges.
func TestResolveBuckets_StrictlyIncreasing(t *testing.T) {
	edges, _ := ResolveBuckets(nil, 30, 50_000) // 50 ms = 50_000 µs
	for i := 1; i < len(edges); i++ {
		if edges[i] <= edges[i-1] {
			t.Errorf("edges not strictly increasing at i=%d: %d <= %d", i, edges[i], edges[i-1])
		}
	}
}

// TestFormatLatency_Decimals locks in the adaptive precision rule.
func TestFormatLatency_Decimals(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{250 * time.Microsecond, "0.250 ms"},
		{1234 * time.Microsecond, "1.23 ms"},
		{12_345 * time.Microsecond, "12.3 ms"},
		{1_500_000 * time.Microsecond, "1500 ms"},
	}
	for _, c := range cases {
		got := FormatLatency(c.d)
		if got != c.want {
			t.Errorf("FormatLatency(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// trim strips trailing padding spaces added by bucketLabels for alignment.
func trim(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
