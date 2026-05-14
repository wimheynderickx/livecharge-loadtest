package metrics

import (
	"sync"
	"time"
)

// bucketDur is the width of one bucket in the throughput ring buffer.
// Ten 100ms buckets give us a 1-second sliding window with 100ms resolution.
const (
	bucketDur   = 100 * time.Millisecond
	bucketCount = 10
)

// Throughput tracks how many messages were recorded in the last second
// (sliding) and exposes that as a msg/sec rate.
//
// The ring buffer of 100 ms buckets is the historic sample mechanism. The
// denominator used by Rate() is min(1 s, elapsed-since-first-record), so
// a scenario that produces 500 events in the first 13 ms doesn't see a
// rate of "500 msg/s" — it sees 500 / 0.013 s ≈ 38 462 msg/s, which is
// the actual rate it was achieving.
type Throughput struct {
	mu       sync.Mutex
	buckets  [bucketCount]int64
	lastSlot int64
	// firstRecord is the wall-clock time of the first Record() call since
	// the last Reset(). Zero-value means "no records yet" and Rate() returns 0.
	firstRecord time.Time
}

// NewThroughput returns a fresh tracker.
func NewThroughput() *Throughput {
	return &Throughput{lastSlot: time.Now().UnixNano() / int64(bucketDur)}
}

// Record marks one event at "now". Safe for concurrent callers.
func (t *Throughput) Record() {
	nowT := time.Now()
	now := nowT.UnixNano() / int64(bucketDur)
	t.mu.Lock()
	if t.firstRecord.IsZero() {
		t.firstRecord = nowT
	}
	t.advance(now)
	t.buckets[now%bucketCount]++
	t.mu.Unlock()
}

// Reset zeroes every bucket and forgets the first-record timestamp so a
// Restart begins with a fresh window.
func (t *Throughput) Reset() {
	t.mu.Lock()
	for i := range t.buckets {
		t.buckets[i] = 0
	}
	t.lastSlot = time.Now().UnixNano() / int64(bucketDur)
	t.firstRecord = time.Time{}
	t.mu.Unlock()
}

// advance zeroes every bucket between the last seen slot and "now".
// Caller must hold t.mu.
func (t *Throughput) advance(now int64) {
	gap := now - t.lastSlot
	if gap <= 0 {
		return
	}
	if gap >= bucketCount {
		// Whole ring rolled over; clear everything.
		for i := range t.buckets {
			t.buckets[i] = 0
		}
	} else {
		// Clear only the buckets the clock has just moved through.
		for i := int64(1); i <= gap; i++ {
			t.buckets[(t.lastSlot+i)%bucketCount] = 0
		}
	}
	t.lastSlot = now
}

// Rate returns the current messages-per-second average.
//
// Denominator: the buckets always cover at most 1 s of past events (older
// ones have been evicted by advance()). Within the first second after the
// first record, the actual elapsed time is shorter than 1 s, so we divide
// by that smaller window instead. This keeps Rate() accurate for short
// bursts that finish in well under 1 s — the original implementation
// returned (events/1s) regardless and under-reported by up to ~80× for
// 10–20 ms runs.
func (t *Throughput) Rate() float64 {
	nowT := time.Now()
	now := nowT.UnixNano() / int64(bucketDur)
	t.mu.Lock()
	t.advance(now)
	var sum int64
	for _, v := range t.buckets {
		sum += v
	}
	firstRec := t.firstRecord
	t.mu.Unlock()

	if firstRec.IsZero() || sum == 0 {
		return 0
	}

	elapsed := nowT.Sub(firstRec)
	if elapsed > time.Second {
		elapsed = time.Second
	}
	if elapsed < time.Microsecond {
		// Floor at 1 µs to avoid divide-by-zero when Rate is called in
		// the same instant as the first Record. The real-world TUI polls
		// at 250 ms so this branch is essentially unreachable in
		// production; tests can hit it via tight loops.
		elapsed = time.Microsecond
	}
	return float64(sum) / elapsed.Seconds()
}
