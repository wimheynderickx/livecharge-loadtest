package metrics

import (
	"testing"
	"time"
)

// TestCollector_MaxAndAvg verifies that the Snapshot exposes a non-zero
// AvgMsgPerSec once elapsed time has accumulated, and that MaxMsgPerSec
// captures the throughput peak observed across snapshots.
func TestCollector_MaxAndAvg(t *testing.T) {
	c := NewCollector("t", "", []float64{50, 95}, 2000)
	c.Start()
	defer c.Stop()

	// Bump the sent counter and feed throughput so Rate() goes positive.
	for i := 0; i < 50; i++ {
		c.SubmitSent()
	}

	// First snapshot — sets the initial max from a positive Rate().
	first := c.Snapshot("RUNNING", 0)
	if first.MsgPerSec <= 0 {
		t.Fatalf("expected positive MsgPerSec on first snapshot, got %f", first.MsgPerSec)
	}
	if first.MaxMsgPerSec < first.MsgPerSec {
		t.Errorf("MaxMsgPerSec should be at least the current rate: max=%f cur=%f", first.MaxMsgPerSec, first.MsgPerSec)
	}

	// Wait so AvgMsgPerSec has a meaningful elapsed denominator.
	time.Sleep(5 * time.Millisecond)
	second := c.Snapshot("RUNNING", 0)
	if second.AvgMsgPerSec <= 0 {
		t.Errorf("expected positive AvgMsgPerSec after elapsed time, got %f", second.AvgMsgPerSec)
	}
	// Max should be sticky across snapshots even if the current rate
	// declines later.
	if second.MaxMsgPerSec < first.MaxMsgPerSec {
		t.Errorf("MaxMsgPerSec should never decrease: %f -> %f", first.MaxMsgPerSec, second.MaxMsgPerSec)
	}

	// Reset should zero everything, including max.
	c.ResetAll()
	zeroed := c.Snapshot("RUNNING", 0)
	if zeroed.MaxMsgPerSec != 0 {
		t.Errorf("expected MaxMsgPerSec to reset to 0, got %f", zeroed.MaxMsgPerSec)
	}
}

// TestCollector_MaxNeverBelowAvg locks in the invariant that the
// throughput peak we report is always at least the lifetime average.
// The avg is a real rate sustained over the run, so the peak can't
// physically be smaller.
func TestCollector_MaxNeverBelowAvg(t *testing.T) {
	c := NewCollector("inv", "", []float64{50}, 2_000_000)
	c.Start()
	defer c.Stop()

	for i := 0; i < 200; i++ {
		c.SubmitSent()
	}
	// Slight delay so AvgMsgPerSec has a meaningful denominator.
	time.Sleep(2 * time.Millisecond)
	snap := c.Snapshot("RUNNING", 0)

	if snap.MaxMsgPerSec < snap.AvgMsgPerSec {
		t.Errorf("invariant violated: MaxMsgPerSec (%.1f) < AvgMsgPerSec (%.1f)",
			snap.MaxMsgPerSec, snap.AvgMsgPerSec)
	}
}

// TestCollector_ElapsedFreezesOnStop guards the bug where Snapshot kept
// adding wall-clock time on top of the banked elapsed total, causing
// AvgMsgPerSec to keep climbing after the scenario finished.
func TestCollector_ElapsedFreezesOnStop(t *testing.T) {
	c := NewCollector("t", "", []float64{50}, 1000)
	c.Start()
	for i := 0; i < 10; i++ {
		c.SubmitSent()
	}
	// Run for a bit so banked elapsed is non-zero.
	time.Sleep(20 * time.Millisecond)
	c.Stop()

	first := c.Snapshot("DONE", 0)
	time.Sleep(30 * time.Millisecond)
	second := c.Snapshot("DONE", 0)

	if second.ElapsedMs != first.ElapsedMs {
		t.Errorf("ElapsedMs must not change after Stop: first=%dms second=%dms",
			first.ElapsedMs, second.ElapsedMs)
	}
	if second.AvgMsgPerSec != first.AvgMsgPerSec {
		t.Errorf("AvgMsgPerSec must not change after Stop: first=%.3f second=%.3f",
			first.AvgMsgPerSec, second.AvgMsgPerSec)
	}
}
