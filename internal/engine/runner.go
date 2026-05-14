package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/metrics"
	"livecharge/loadtest/internal/template"
	"livecharge/loadtest/internal/transport"

	"github.com/BurntSushi/toml"
)

// Runner is the concrete ScenarioRunner implementation.
type Runner struct {
	cfg       *config.ScenarioConfig
	md        toml.MetaData
	transport transport.Transport
	factory   *template.ContextFactory
	collector *metrics.Collector
	generator *LoadGenerator
	preCalc   map[string][]byte

	// Pre-resolved latency-tab bucket layout. Edges and labels are
	// computed once in NewRunner; the TUI reads them every frame.
	bucketEdges  []int64
	bucketLabels []string

	mu    sync.Mutex
	state State
	// ctxRoot is the parent context that owns the running workers.
	// It is recreated on each Start/Resume so a Stop fully cancels them.
	ctxRoot       context.Context
	ctxRootCancel context.CancelFunc
	doneCh        chan struct{}
	// stopCollectorOnce ensures collector.Stop is called exactly once per
	// lifecycle even when natural completion and an external Stop() race.
	// Reset in startInternal before goroutines are spawned.
	stopCollectorOnce sync.Once

	// onTerminal is invoked exactly once per lifecycle, after the runner
	// reaches DONE or ERROR. Callers set it before Start() — typically
	// the CLI uses it to fire the post-run email. nil = no callback.
	// The callback runs on the background watcher goroutine; implementers
	// must not block (spawn their own goroutine if they need to do I/O).
	onTerminal func(State)

	// onStart is invoked once per lifecycle when the runner transitions
	// IDLE → RUNNING from a Start() call. Resume() and the inner re-start
	// of Restart() are explicitly excluded — those continue or refresh
	// a prior run rather than initiating one. nil = no callback. The
	// callback is fired on a dedicated goroutine so a slow handler can't
	// stall the actual generator startup.
	onStart func()
}

// NewRunner builds a Runner ready to Start.
//
// The returned Runner takes ownership of the transport (it will Close it on
// Stop) and the supplied configs.
func NewRunner(loaded *config.LoadedScenario) (*Runner, error) {
	if loaded == nil || loaded.Config == nil {
		return nil, errors.New("engine: nil scenario")
	}
	cfg := loaded.Config

	tr, err := newTransport(cfg.Transport)
	if err != nil {
		return nil, fmt.Errorf("engine: build transport: %w", err)
	}

	factory, err := template.NewContextFactory(cfg.Context, loaded.MetaData)
	if err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("engine: build context factory: %w", err)
	}

	pre, err := PreCalc(cfg, factory)
	if err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("engine: pre-calc: %w", err)
	}

	// The histogram (and matching bucket layer) work in microseconds so
	// sub-millisecond latencies show up faithfully.
	maxLatencyUs := cfg.Load.ResponseTimeout.Duration.Microseconds()
	if maxLatencyUs < 1 {
		maxLatencyUs = 1
	}
	col := metrics.NewCollector(cfg.Scenario.Name, cfg.Scenario.Description, cfg.Metrics.Percentiles, maxLatencyUs)
	gen := NewLoadGenerator(cfg.Load)

	// Resolve the latency-tab bucket layout once at construction time.
	// The TUI reads the cached edges+labels every frame; recomputing would
	// be cheap but pointless.
	bucketEdges, bucketLabels := metrics.ResolveBuckets(
		cfg.Metrics.BucketEdgesMs,
		cfg.Metrics.BucketCount,
		maxLatencyUs,
	)

	return &Runner{
		cfg:          cfg,
		md:           loaded.MetaData,
		transport:    tr,
		factory:      factory,
		collector:    col,
		generator:    gen,
		preCalc:      pre.Bodies,
		state:        StateIdle,
		bucketEdges:  bucketEdges,
		bucketLabels: bucketLabels,
	}, nil
}

// Name returns the scenario name.
func (r *Runner) Name() string { return r.cfg.Scenario.Name }

// Description returns the scenario description from the [scenario] block.
// Used by the email feature to populate the {{.Scenario.Description}}
// placeholder.
func (r *Runner) Description() string { return r.cfg.Scenario.Description }

// SetOnTerminal registers a callback invoked exactly once when the runner
// reaches DONE or ERROR. The callback runs on a background goroutine and
// must not block — kick off any I/O in another goroutine. Setting it to
// nil disables the callback.
func (r *Runner) SetOnTerminal(cb func(State)) {
	r.mu.Lock()
	r.onTerminal = cb
	r.mu.Unlock()
}

// SetOnStart registers a callback invoked when the runner transitions
// IDLE → RUNNING from a fresh Start() (not Resume; the inner re-start of
// Restart still fires because Restart resets state to IDLE first). The
// callback runs on a dedicated goroutine, so a slow handler doesn't
// block the generator's startup. Setting it to nil disables the
// callback.
func (r *Runner) SetOnStart(cb func()) {
	r.mu.Lock()
	r.onStart = cb
	r.mu.Unlock()
}

// State returns the current lifecycle state.
func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Snapshot exposes the current metrics with the state name attached.
func (r *Runner) Snapshot() metrics.Snapshot {
	r.mu.Lock()
	st := r.state
	r.mu.Unlock()
	return r.collector.Snapshot(st.String(), r.cfg.Load.TotalMessages)
}

// Buckets exposes the global latency histogram aggregated into the
// caller-supplied edge list. Used by the TUI Latency tab.
func (r *Runner) Buckets(edges []int64) []int64 {
	return r.collector.Buckets(edges)
}

// LatencyBuckets returns the pre-resolved bucket edges and matching labels
// for this scenario's [metrics] configuration. The slices are owned by the
// Runner; callers must not modify them.
func (r *Runner) LatencyBuckets() (edges []int64, labels []string) {
	return r.bucketEdges, r.bucketLabels
}

// LogCh returns the underlying log channel so the TUI can subscribe.
func (r *Runner) LogCh() <-chan string {
	return r.collector.LogCh()
}

// LogTail returns up to n recent log lines from the collector's ring
// buffer. Independent from LogCh — callers can use both. Empty when the
// scenario has produced no log output yet.
func (r *Runner) LogTail(n int) []string {
	return r.collector.LogTail(n)
}

// DoneCh returns a channel that closes when the runner finishes on its own
// (load limit reached). Callers (the headless runtime, the TUI) use it to
// detect completion without polling.
func (r *Runner) DoneCh() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.doneCh
}

// Start kicks off the load generator if the runner is IDLE.
func (r *Runner) Start() error {
	return r.startInternal(false)
}

// Resume continues a STOPPED runner, keeping all banked metrics.
func (r *Runner) Resume() error {
	r.mu.Lock()
	if r.state != StateStopped {
		r.mu.Unlock()
		return fmt.Errorf("cannot resume from state %s", r.state)
	}
	r.mu.Unlock()
	return r.startInternal(true)
}

// Restart resets all metrics and run-state, then Starts.
func (r *Runner) Restart() error {
	r.mu.Lock()
	switch r.state {
	case StateRunning:
		r.mu.Unlock()
		if err := r.Stop(); err != nil {
			return err
		}
		r.mu.Lock()
	}
	r.collector.ResetAll()
	r.generator.Reset()
	r.state = StateIdle
	r.mu.Unlock()
	return r.startInternal(false)
}

// startInternal is the shared body of Start and Resume.
// resume controls whether the collector is started fresh or continues with
// previously banked metrics.
func (r *Runner) startInternal(resume bool) error {
	r.mu.Lock()
	if r.state == StateRunning {
		r.mu.Unlock()
		return fmt.Errorf("scenario %q is already running", r.cfg.Scenario.Name)
	}
	if r.state == StateDone || r.state == StateError {
		r.mu.Unlock()
		return fmt.Errorf("scenario %q has terminated (state=%s); call Restart to run again", r.cfg.Scenario.Name, r.state)
	}

	r.collector.Start()
	r.stopCollectorOnce = sync.Once{} // reset for this new lifecycle
	parent, cancel := context.WithCancel(context.Background())
	r.ctxRoot = parent
	r.ctxRootCancel = cancel
	r.doneCh = make(chan struct{})
	prev := r.state
	r.state = StateRunning
	startCb := r.onStart
	r.mu.Unlock()

	r.collector.Logf("STATE %s -> RUNNING", prev)

	// Fire OnStart on fresh starts only — resume continues a prior run
	// and the inner re-start of Restart still qualifies because Restart
	// resets state to IDLE before calling startInternal. Async so a slow
	// callback doesn't delay the generator boot.
	if startCb != nil && !resume && prev == StateIdle {
		go startCb()
	}

	// sessionFn creates a fresh session each call. Building it here (not
	// inside the worker) keeps the LoadGenerator unaware of session
	// internals.
	sessionFn := func(ctx context.Context) {
		s, err := newSession(r.cfg, r.transport, r.factory, r.preCalc, r.collector)
		if err != nil {
			r.collector.Submit(metrics.StepResult{Err: err})
			return
		}
		s.run(ctx)
	}

	// We pass a nil onDone callback because the natural-completion signal
	// would fire from inside the worker at the moment the limit is hit —
	// before sibling workers' in-flight sessions complete and before the
	// collector has drained their StepResults. The background watcher
	// below transitions to DONE only after generator.Wait + collector.Stop,
	// which is the only point at which the metrics are guaranteed final.
	r.generator.Start(parent, sessionFn, nil)

	// Background watcher: when all workers exit AND the collector has
	// drained, settle the final state. This is the single source of truth
	// for DONE.
	go func() {
		r.generator.Wait()
		r.stopCollectorOnce.Do(r.collector.Stop)
		r.mu.Lock()
		// If the state is still RUNNING it means we exited via natural
		// completion (limit reached / duration elapsed). External Stop
		// would have already set StateStopped.
		var terminal State = -1
		if r.state == StateRunning {
			r.state = StateDone
			close(r.doneCh)
			terminal = StateDone
		}
		cb := r.onTerminal
		r.mu.Unlock()

		// Fire the lifecycle callback once we're outside the lock so a
		// slow handler can't deadlock the runner. We only fire on DONE
		// here; ERROR transitions live in the worker error path (added
		// alongside this hook). External STOP intentionally does NOT
		// fire the callback because the user already knows the run
		// ended — they pressed 's'.
		if cb != nil && terminal != -1 {
			cb(terminal)
		}
	}()

	_ = resume // resume is implicit in not calling collector.ResetAll
	return nil
}

// Stop drains in-flight sessions and parks the runner in STOPPED state.
func (r *Runner) Stop() error {
	r.mu.Lock()
	if r.state != StateRunning {
		r.mu.Unlock()
		return fmt.Errorf("cannot stop from state %s", r.state)
	}
	r.state = StateStopped
	cancel := r.ctxRootCancel
	r.mu.Unlock()

	cancel()
	r.generator.Wait()
	r.collector.Logf("STATE RUNNING -> STOPPED")
	r.stopCollectorOnce.Do(r.collector.Stop)
	return nil
}

// Close releases the transport. After Close the runner must not be used.
func (r *Runner) Close() error {
	return r.transport.Close()
}
