package metrics

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// StepResult is one event submitted by a session after each step.
//
// Submitting is non-blocking from the session's point of view: if the
// channel is full (collector falling behind), the result is dropped and
// counted in DroppedEvents. Dropping is a deliberate trade-off; back-
// pressure into the engine would distort latency numbers.
type StepResult struct {
	Latency time.Duration
	// Err is set for any failure: transport error, timeout, render failure.
	Err error
	// PredicateName is the accounting key of the predicate that matched
	// this step. Empty when no predicate fired (or the step had none).
	PredicateName string
	// FireAndForget steps still produce a StepResult so we count throughput,
	// but they have no Latency and are not added to histograms.
	FireAndForget bool
}

// Collector receives StepResult events from sessions and updates counters
// and histograms in a single goroutine.
type Collector struct {
	scenarioName        string
	scenarioDescription string
	percentiles         []float64

	// Channel buffer is large so brief bursts don't drop events.
	results chan StepResult

	// Counters — atomic so Snapshot can read without taking the goroutine
	// out of its hot loop.
	sent     atomic.Int64
	received atomic.Int64
	errors   atomic.Int64
	dropped  atomic.Int64

	// Histograms
	mu             sync.Mutex // guards predicates map (entries are added on demand)
	global         *LatencyHistogram
	predicates     map[string]*predicateBucket
	predicateNames []string // stable order for snapshot output

	throughput *Throughput

	// maxRateBits holds the highest MsgPerSec value seen across all
	// Snapshot() calls, encoded with math.Float64bits so a single atomic
	// op covers the CAS update. Reset to 0 by ResetAll().
	maxRateBits atomic.Uint64

	// startTime is when Start() was last called. elapsedBank holds time
	// banked by previous Stop() calls (for Resume support).
	startTimeMu sync.Mutex
	startTime   time.Time
	elapsedBank time.Duration

	// logCh fans out human-readable events (step errors, state changes) so
	// the TUI Log tab and --log-file can show them. Sends are non-blocking;
	// a full buffer drops the message.
	logCh chan string

	// logRingMu and logRing keep a bounded copy of the most recent log
	// lines so a non-channel consumer (e.g. the post-run email attachment
	// builder) can read them at any moment without racing with the TUI.
	// The ring is independent of logCh and survives a full channel.
	logRingMu sync.Mutex
	logRing   []string

	done    chan struct{}
	doneAck chan struct{}
}

// logRingSize bounds the in-memory log ring. 2000 lines comfortably covers
// even a noisy scenario; the per-line overhead (~100 bytes) keeps the
// total under 200 KB even at saturation, well within reason for an email
// attachment.
const logRingSize = 2000

type predicateBucket struct {
	hist  *LatencyHistogram
	count atomic.Int64
}

// NewCollector builds a collector. maxLatencyMs is the upper bound for the
// HDR histograms; set it to the scenario's response_timeout in milliseconds.
func NewCollector(scenarioName, scenarioDescription string, percentiles []float64, maxLatencyMs int64) *Collector {
	return &Collector{
		scenarioName:        scenarioName,
		scenarioDescription: scenarioDescription,
		percentiles:  percentiles,
		results:      make(chan StepResult, 4096),
		global:       NewLatencyHistogram(maxLatencyMs),
		predicates:   make(map[string]*predicateBucket),
		throughput:   NewThroughput(),
		logCh:        make(chan string, 256),
		done:         make(chan struct{}),
		doneAck:      make(chan struct{}),
	}
}

// LogCh returns a read-only channel of human-readable event strings.
// The TUI Log tab and the --log-file writer drain this channel.
func (c *Collector) LogCh() <-chan string { return c.logCh }

// Logf publishes one log line. Non-blocking: if the buffer is full the
// message is dropped rather than back-pressuring the engine. The line is
// also pushed into the bounded log ring so non-channel consumers (the
// email attachment builder) can read recent history at any moment.
func (c *Collector) Logf(format string, args ...any) {
	msg := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	select {
	case c.logCh <- msg:
	default:
	}
	c.logRingMu.Lock()
	c.logRing = append(c.logRing, msg)
	if len(c.logRing) > logRingSize {
		// Drop oldest entries; copy to a new backing array so the underlying
		// storage doesn't grow unbounded as Append slices off the front.
		drop := len(c.logRing) - logRingSize
		c.logRing = append([]string{}, c.logRing[drop:]...)
	}
	c.logRingMu.Unlock()
}

// LogTail returns up to the most recent n log lines from the ring buffer.
// Pass 0 to get the whole buffer. The returned slice is a fresh copy and
// safe for the caller to retain.
//
// Used by the mail subsystem when building the scenario-log attachment.
func (c *Collector) LogTail(n int) []string {
	c.logRingMu.Lock()
	defer c.logRingMu.Unlock()
	if len(c.logRing) == 0 {
		return nil
	}
	start := 0
	if n > 0 && n < len(c.logRing) {
		start = len(c.logRing) - n
	}
	out := make([]string, len(c.logRing)-start)
	copy(out, c.logRing[start:])
	return out
}

// Start launches the drain goroutine and stamps the start time so Elapsed()
// is correct. Safe to call again after a Stop+reset (used by Restart).
func (c *Collector) Start() {
	c.startTimeMu.Lock()
	c.startTime = time.Now()
	c.startTimeMu.Unlock()

	go c.drain()
}

// Stop signals the drain goroutine to finish, waits for it, and banks the
// elapsed run time. After Stop, Submit must not be called until Start is
// invoked again.
func (c *Collector) Stop() {
	close(c.done)
	<-c.doneAck

	c.startTimeMu.Lock()
	c.elapsedBank += time.Since(c.startTime)
	// Clear startTime so Snapshot() stops adding live wall-clock time on
	// top of the banked total. A subsequent Start() will stamp a new
	// startTime; without this, elapsed (and therefore AvgMsgPerSec) keeps
	// growing after a scenario finishes.
	c.startTime = time.Time{}
	c.startTimeMu.Unlock()

	// Recreate the channels so a subsequent Start can be called for resume.
	c.done = make(chan struct{})
	c.doneAck = make(chan struct{})
}

// ResetAll wipes counters and histograms so a Restart() begins from zero.
func (c *Collector) ResetAll() {
	c.sent.Store(0)
	c.received.Store(0)
	c.errors.Store(0)
	c.dropped.Store(0)
	c.maxRateBits.Store(0)
	c.throughput.Reset()

	c.global.Reset()
	c.mu.Lock()
	for _, b := range c.predicates {
		b.hist.Reset()
		b.count.Store(0)
	}
	c.mu.Unlock()

	c.startTimeMu.Lock()
	c.elapsedBank = 0
	c.startTimeMu.Unlock()
}

// SubmitSent is called when a request is dispatched to the wire. We update
// the sent counter eagerly so the TUI shows in-flight requests even before
// their replies come back.
func (c *Collector) SubmitSent() {
	c.sent.Add(1)
	c.throughput.Record()
}

// Submit posts a step result for asynchronous processing.
// Non-blocking; results are dropped if the buffer is full.
func (c *Collector) Submit(r StepResult) {
	select {
	case c.results <- r:
	default:
		c.dropped.Add(1)
	}
}

// drain is the single-writer goroutine. It loops over the results channel
// until done is closed and the channel has been emptied.
func (c *Collector) drain() {
	for {
		select {
		case r := <-c.results:
			c.apply(r)
		case <-c.done:
			// Drain whatever's left before acknowledging.
			for {
				select {
				case r := <-c.results:
					c.apply(r)
				default:
					close(c.doneAck)
					return
				}
			}
		}
	}
}

// apply integrates one StepResult into the running stats.
func (c *Collector) apply(r StepResult) {
	if r.Err != nil {
		c.errors.Add(1)
		c.Logf("ERROR %s", r.Err.Error())
		return
	}
	c.received.Add(1)

	if r.FireAndForget {
		return
	}

	c.global.Record(r.Latency)

	if r.PredicateName != "" {
		b := c.getPredicate(r.PredicateName)
		b.hist.Record(r.Latency)
		b.count.Add(1)
	}
}

// getPredicate returns the bucket for name, creating it on first sight.
func (c *Collector) getPredicate(name string) *predicateBucket {
	c.mu.Lock()
	b, ok := c.predicates[name]
	if !ok {
		b = &predicateBucket{
			hist: NewLatencyHistogram(c.global.maxUs()),
		}
		c.predicates[name] = b
		c.predicateNames = append(c.predicateNames, name)
	}
	c.mu.Unlock()
	return b
}

// Buckets returns the global histogram aggregated into the supplied edges.
// Used by the TUI Latency tab.
func (c *Collector) Buckets(edges []int64) []int64 {
	return c.global.Buckets(edges)
}

// Snapshot builds a read-consistent view. Safe to call from any goroutine.
func (c *Collector) Snapshot(stateName string, target int64) Snapshot {
	c.startTimeMu.Lock()
	elapsed := c.elapsedBank
	if !c.startTime.IsZero() {
		// startTime is zero only between New and Start.
		elapsed += time.Since(c.startTime)
	}
	c.startTimeMu.Unlock()

	predSnap := make(map[string]PredicateStats)
	c.mu.Lock()
	names := append([]string{}, c.predicateNames...)
	c.mu.Unlock()
	for _, name := range names {
		c.mu.Lock()
		b := c.predicates[name]
		c.mu.Unlock()
		predSnap[name] = PredicateStats{
			Count:       b.count.Load(),
			Percentiles: b.hist.PercentileMap(c.percentiles),
		}
	}

	cur := c.throughput.Rate()
	sent := c.sent.Load()
	avg := lifetimeAvg(sent, elapsed)
	// Max should never be lower than the lifetime average — the average
	// is a real rate sustained over [start..now], so by definition the
	// peak must be at least that high. observeMax takes the larger of
	// the two candidates as the new sample to guard against sampling
	// races and unfilled-window aliasing in Throughput.Rate().
	candidate := cur
	if avg > candidate {
		candidate = avg
	}
	maxRate := c.observeMax(candidate)

	return Snapshot{
		ScenarioName:        c.scenarioName,
		ScenarioDescription: c.scenarioDescription,
		StateName:    stateName,
		ElapsedMs:    elapsed.Milliseconds(),
		TotalTarget:  target,
		Sent:         sent,
		Received:     c.received.Load(),
		Errors:       c.errors.Load(),
		MsgPerSec:    cur,
		MaxMsgPerSec: maxRate,
		AvgMsgPerSec: avg,
		Percentiles:  c.global.PercentileMap(c.percentiles),
		Predicates:   predSnap,
	}
}

// observeMax updates maxRateBits to max(prev, cur) and returns the result.
// We encode the float64 as bits and use a CAS retry loop because atomic
// has no native float64 — bits + CAS is the standard pattern.
func (c *Collector) observeMax(cur float64) float64 {
	for {
		prevBits := c.maxRateBits.Load()
		prev := math.Float64frombits(prevBits)
		if cur <= prev {
			return prev
		}
		if c.maxRateBits.CompareAndSwap(prevBits, math.Float64bits(cur)) {
			return cur
		}
	}
}

// lifetimeAvg is sent divided by elapsed seconds. Returns 0 until the
// scenario has been running long enough to give a meaningful number.
func lifetimeAvg(sent int64, elapsed time.Duration) float64 {
	if elapsed < time.Millisecond {
		return 0
	}
	return float64(sent) / elapsed.Seconds()
}

// maxUs returns the histogram's configured upper bound (in microseconds),
// used when adding a new per-predicate histogram with the same range.
func (lh *LatencyHistogram) maxUs() int64 {
	lh.mu.Lock()
	v := lh.h.HighestTrackableValue()
	lh.mu.Unlock()
	return v
}
