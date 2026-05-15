package predicate

import (
	"strconv"
	"sync"

	"livecharge/loadtest/internal/config"
)

// Result is the dispatch outcome shared by classic + expr predicates.
type Result struct {
	Matched  string
	NextStep string
	HasMatch bool
}

// Evaluate is the unified entry point that callers (the engine session)
// use to walk a step's predicates. It dispatches each predicate to the
// right implementation:
//
//   - op="expr" runs the pre-compiled Compiled program against ctx.
//   - any other op runs the classic matcher against a session-shaped map
//     synthesised from ctx so existing classic-op scenarios keep working
//     unchanged.
//
// compiled[i] is the *Compiled for preds[i] when preds[i].Op == "expr",
// nil otherwise. Builders (engine.NewRunner) must populate compiled in
// parallel with preds.
func Evaluate(preds []config.PredicateConfig, compiled []*Compiled, ctx Context) Result {
	for i, p := range preds {
		if p.Op == "expr" {
			if i >= len(compiled) || compiled[i] == nil {
				// Defensive — engine.NewRunner should populate every slot.
				continue
			}
			matched, err := Eval(compiled[i], ctx)
			if err != nil {
				logEvalErrorOnce(p.Name, err)
				continue
			}
			if matched {
				return Result{Matched: p.Name, NextStep: p.NextStep, HasMatch: true}
			}
			continue
		}
		// Classic op: synthesise a session-like map from ctx so the
		// existing matcher still works (no behaviour change).
		mergedSession := make(map[string]any, len(ctx.Session)+1)
		for k, v := range ctx.Session {
			mergedSession[k] = v
		}
		if ctx.Status != 0 {
			mergedSession["status"] = strconv.Itoa(ctx.Status)
		}
		if matchesClassic(p, mergedSession) {
			return Result{Matched: p.Name, NextStep: p.NextStep, HasMatch: true}
		}
	}
	return Result{}
}

var evalErrSeen sync.Map // map[string]struct{} keyed by "<predicate>|<err>"

// logEvalErrorOnce fires OnEvalError once per (predicate, error) pair
// per process lifetime. Repeats are silenced to keep run logs readable
// when an expr is consistently buggy on a hot path.
//
// OnEvalError is set by the engine.Runner at construction time so the
// log lines land in the right scenario's log buffer. nil is a no-op.
func logEvalErrorOnce(predicate string, err error) {
	key := predicate + "|" + err.Error()
	if _, loaded := evalErrSeen.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if OnEvalError != nil {
		OnEvalError(predicate, err)
	}
}

// OnEvalError is invoked the first time a given (predicate, error) pair
// is seen at runtime. Engine code installs a hook that logs to the
// per-scenario collector. nil disables logging.
//
// NOTE: OnEvalError is a package-level variable. When multiple Runner
// instances exist concurrently, the last-wired runner's hook wins. This
// is acceptable for 0.2 — each runner has its own collector but
// runtime expr errors are rare, and "close enough" logging is fine.
var OnEvalError func(predicate string, err error)
