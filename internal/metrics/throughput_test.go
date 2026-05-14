package metrics

import (
	"testing"
	"time"
)

// TestThroughput_ShortWindow guards the bug where a burst of N events in
// less than 1 s was reported as a rate of N/sec instead of N / elapsed_s.
// We record 1000 events in quick succession and assert Rate() reports a
// number well above 1000 (which would be the old buggy floor).
func TestThroughput_ShortWindow(t *testing.T) {
	tp := NewThroughput()

	start := time.Now()
	for i := 0; i < 1000; i++ {
		tp.Record()
	}
	got := tp.Rate()
	elapsed := time.Since(start)

	// Sanity floor: at the very least, 1000 events / 1 s = 1000 (the old buggy value).
	// We want substantially more because elapsed is tiny.
	wantAtLeast := float64(1000) / elapsed.Seconds() / 2 // factor 2 slack
	if got < wantAtLeast {
		t.Errorf("Rate() = %.1f; want at least %.1f (events=%d elapsed=%s)",
			got, wantAtLeast, 1000, elapsed)
	}
	if got <= 1000 {
		t.Errorf("Rate() = %.1f; the short-window fix should yield well above 1000", got)
	}
}

// TestThroughput_ResetClearsFirstRecord asserts that the elapsed-window
// math starts fresh after Reset (used by Restart).
func TestThroughput_ResetClearsFirstRecord(t *testing.T) {
	tp := NewThroughput()
	tp.Record()
	if tp.Rate() == 0 {
		t.Error("expected non-zero rate after first Record")
	}
	tp.Reset()
	if got := tp.Rate(); got != 0 {
		t.Errorf("Rate() after Reset = %.3f; want 0", got)
	}
}
