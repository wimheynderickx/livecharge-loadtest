package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"livecharge/loadtest/internal/config"
)

// LoadGenerator coordinates the goroutine pool that drives sessions and the
// token-bucket limiter that paces them.
//
// Two contexts split the cancellation surface:
//
//   - sessionCtx — passed to sessionFn and therefore to in-flight Send calls.
//     Cancelled only on external Stop. We never cancel it when load
//     completes naturally (total_messages reached or duration elapsed),
//     because doing so aborts whichever requests other workers happen to
//     have in flight at that moment with a "context canceled" error.
//
//   - acceptCtx  — used by workers for the limiter.Wait and the loop check.
//     Cancelled on natural completion AND derived from sessionCtx (so an
//     external Stop also cancels it). When acceptCtx is cancelled, workers
//     stop *starting* new sessions but their current session, if any, runs
//     to completion against sessionCtx.
type LoadGenerator struct {
	cfg     config.LoadConfig
	limiter *rate.Limiter

	mu        sync.Mutex
	running   bool
	startTime time.Time

	// sessionCancel cancels sessionCtx and is what Stop calls externally.
	sessionCancel context.CancelFunc
	// acceptCancel cancels acceptCtx and is called on natural completion.
	acceptCancel context.CancelFunc

	wg sync.WaitGroup

	// totalSent is the lifetime count of session-starts across Start/Resume
	// cycles. Compared against cfg.TotalMessages to decide when to stop.
	totalSent atomic.Int64

	// elapsedBank is the total run time of all completed Start cycles.
	elapsedBank time.Duration

	// onDoneOnce ensures the natural-completion callback fires at most once
	// across the lifetime of one Start call.
	onDoneOnce sync.Once
}

// NewLoadGenerator builds a generator without starting it.
func NewLoadGenerator(cfg config.LoadConfig) *LoadGenerator {
	var limiter *rate.Limiter
	if cfg.Rate > 0 {
		// Burst = 1 so each session start needs a fresh token; this gives
		// us strict rate pacing rather than huge bursts at startup.
		limiter = rate.NewLimiter(rate.Limit(cfg.Rate), 1)
	} else {
		// Unlimited: an InfRate limiter never blocks.
		limiter = rate.NewLimiter(rate.Inf, 1)
	}
	return &LoadGenerator{cfg: cfg, limiter: limiter}
}

// Start launches the worker goroutines. sessionFn drives one session per
// call; it is expected to block until the session ends.
//
// onDone fires (once) when the generator stops on its own (limit reached
// or duration elapsed). It does not fire when Stop is called externally.
func (g *LoadGenerator) Start(parent context.Context, sessionFn func(context.Context), onDone func()) {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	sessionCtx, sessionCancel := context.WithCancel(parent)
	acceptCtx, acceptCancel := context.WithCancel(sessionCtx)
	g.sessionCancel = sessionCancel
	g.acceptCancel = acceptCancel
	g.running = true
	g.startTime = time.Now()
	g.onDoneOnce = sync.Once{}
	g.mu.Unlock()

	signalDone := func() {
		g.onDoneOnce.Do(func() {
			if onDone != nil {
				onDone()
			}
		})
	}

	// Duration watcher: cancels acceptCtx (stops new sessions) when the
	// time budget is exhausted. In-flight sessions keep their sessionCtx
	// and run to completion.
	if g.cfg.Duration.Duration > 0 {
		remaining := g.cfg.Duration.Duration - g.elapsedBank
		if remaining <= 0 {
			acceptCancel()
			signalDone()
		} else {
			go func() {
				select {
				case <-time.After(remaining):
					acceptCancel()
					signalDone()
				case <-acceptCtx.Done():
				}
			}()
		}
	}

	// Worker pool.
	for i := 0; i < g.cfg.Concurrency; i++ {
		g.wg.Add(1)
		go g.worker(acceptCtx, sessionCtx, sessionFn, signalDone)
	}
}

// worker is one slot in the pool: wait for a token, run a session, loop.
//
// acceptCtx gates the accept loop and limiter wait; it is cancelled when
// the generator finishes naturally OR when external Stop cancels session.
// sessionCtx is passed to the session and is cancelled only by external Stop,
// so a natural-end signal lets the current session finish cleanly.
func (g *LoadGenerator) worker(acceptCtx, sessionCtx context.Context, sessionFn func(context.Context), signalDone func()) {
	defer g.wg.Done()
	for {
		if acceptCtx.Err() != nil {
			return
		}

		if err := g.limiter.Wait(acceptCtx); err != nil {
			return // accept cancelled
		}

		// Check the total-messages cap BEFORE starting the session so we
		// don't overshoot by Concurrency.
		if g.cfg.TotalMessages > 0 {
			if n := g.totalSent.Add(1); n > g.cfg.TotalMessages {
				// Tell the rest of the pool to stop accepting new sessions.
				// We do NOT cancel sessionCtx — other workers might be in
				// the middle of an HTTP/NATS call right now, and cancelling
				// would abort them with "context canceled".
				g.acceptCancel()
				signalDone()
				return
			}
		} else {
			g.totalSent.Add(1)
		}

		sessionFn(sessionCtx)
	}
}

// Stop cancels the workers and waits for them to drain.
//
// Stop is the external "abort now" path: it cancels sessionCtx so in-flight
// requests fail fast with context-cancelled. Use it for user-initiated
// interruption; the natural-completion path (total_messages/duration) goes
// through acceptCancel instead.
func (g *LoadGenerator) Stop() {
	g.mu.Lock()
	if !g.running {
		g.mu.Unlock()
		return
	}
	g.sessionCancel()
	g.running = false
	g.elapsedBank += time.Since(g.startTime)
	g.mu.Unlock()
	g.wg.Wait()
}

// Wait blocks until the workers exit on their own (duration/total reached).
func (g *LoadGenerator) Wait() {
	g.wg.Wait()
	g.mu.Lock()
	if g.running {
		g.elapsedBank += time.Since(g.startTime)
		g.running = false
	}
	g.mu.Unlock()
}

// Elapsed returns the total run time across Start/Resume cycles.
func (g *LoadGenerator) Elapsed() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running {
		return g.elapsedBank + time.Since(g.startTime)
	}
	return g.elapsedBank
}

// SentSoFar returns the running count of session-starts.
func (g *LoadGenerator) SentSoFar() int64 {
	return g.totalSent.Load()
}

// Reset clears banked time and counter for a Restart.
func (g *LoadGenerator) Reset() {
	g.mu.Lock()
	g.elapsedBank = 0
	g.startTime = time.Time{}
	g.mu.Unlock()
	g.totalSent.Store(0)
}
