package template

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"

	"livecharge/loadtest/internal/config"
)

// Context holds the namespaces available inside a template. We use a plain
// map[string]any rather than a struct because Go's text/template package
// looks up struct fields by exact case-sensitive name, but TOML templates
// use lowercase keys ({{.ctx.X}}). A map makes the keys exactly what the
// template asks for.
//
// Recognised keys:
//
//   - "ctx"     — initialised once per session by ContextFactory.Snapshot
//   - "session" — populated as the session progresses by Extractor results
//
// Mock-server templates use the key "extracted" instead (see Extracted).
type Context map[string]any

// NewContext returns a Context with empty ctx/session maps. Callers populate
// these maps; the keys are the template field names.
func NewContext() Context {
	return Context{
		"ctx":     map[string]any{},
		"session": map[string]any{},
	}
}

// Extracted is the namespace seen by mock-server templates. Same reasoning
// as Context: a map keyed "extracted" makes {{.extracted.X}} resolve.
type Extracted map[string]any

// NewExtracted wraps an already-built field map in the right shape for the
// renderer.
func NewExtracted(fields map[string]any) Extracted {
	return Extracted{"extracted": fields}
}

// generator is the interface implemented by each context-value producer.
// Static values, sequences, random ranges, and random picks all implement it.
type generator interface {
	// next returns the value to use for the new session.
	// It must be safe to call concurrently from multiple sessions.
	next() any
	// isStatic reports whether the generator always returns the same value.
	// This is used by IsStatic (precalc.go) to decide whether a whole
	// template can be pre-rendered at startup.
	isStatic() bool
}

// ContextFactory builds .ctx maps for new sessions. One factory per scenario;
// shared across all concurrent sessions of that scenario.
type ContextFactory struct {
	gens map[string]generator
}

// Definitions returns a snapshot of the generator types keyed by their
// .ctx name. Used by IsStatic in precalc.go to decide whether each referenced
// context variable can be treated as a constant.
func (f *ContextFactory) Definitions() map[string]bool {
	out := make(map[string]bool, len(f.gens))
	for k, g := range f.gens {
		out[k] = g.isStatic()
	}
	return out
}

// NewContextFactory inspects every entry in the raw [context] map and builds
// the corresponding generator. md is the toml.MetaData returned alongside the
// scenario decode; it is needed to decode each toml.Primitive on demand.
//
// Decoding strategy per entry:
//  1. Try as string, int64, float64, bool — these are static scalars.
//  2. Otherwise decode as ContextValueConfig and pick a generator by Type.
func NewContextFactory(raw map[string]toml.Primitive, md toml.MetaData) (*ContextFactory, error) {
	gens := make(map[string]generator, len(raw))
	for key, prim := range raw {
		// --- attempt scalar decodes first ---
		var sv string
		if err := md.PrimitiveDecode(prim, &sv); err == nil {
			gens[key] = staticGen{value: sv}
			continue
		}
		var iv int64
		if err := md.PrimitiveDecode(prim, &iv); err == nil {
			gens[key] = staticGen{value: iv}
			continue
		}
		var fv float64
		if err := md.PrimitiveDecode(prim, &fv); err == nil {
			gens[key] = staticGen{value: fv}
			continue
		}
		var bv bool
		if err := md.PrimitiveDecode(prim, &bv); err == nil {
			gens[key] = staticGen{value: bv}
			continue
		}

		// --- fall back to generator table ---
		var def config.ContextValueConfig
		if err := md.PrimitiveDecode(prim, &def); err != nil {
			return nil, fmt.Errorf("context.%s: cannot decode (not scalar or generator): %w", key, err)
		}

		g, err := makeGenerator(key, def)
		if err != nil {
			return nil, err
		}
		gens[key] = g
	}
	return &ContextFactory{gens: gens}, nil
}

// makeGenerator picks the right generator implementation from a parsed
// ContextValueConfig.
func makeGenerator(key string, def config.ContextValueConfig) (generator, error) {
	switch def.Type {
	case "sequence":
		step := def.Step
		if step == 0 {
			step = 1
		}
		// We start the atomic counter at (start - step) so that the very
		// first call to next() lands on start.
		g := &sequenceGen{step: step}
		g.counter.Store(def.Start - step)
		return g, nil
	case "random_range":
		if def.Min >= def.Max {
			return nil, fmt.Errorf("context.%s: random_range requires min < max", key)
		}
		return &randomRangeGen{
			min: def.Min,
			max: def.Max,
			rng: newLocked(),
		}, nil
	case "random_pick":
		if len(def.Values) == 0 {
			return nil, fmt.Errorf("context.%s: random_pick requires at least one value", key)
		}
		return &randomPickGen{
			values: append([]string{}, def.Values...),
			rng:    newLocked(),
		}, nil
	default:
		return nil, fmt.Errorf("context.%s: unknown generator type %q", key, def.Type)
	}
}

// Snapshot produces a fresh .ctx map for one session. Calling code passes
// the result inside a Context to the Renderer.
//
// Safe to call from many goroutines at once; generator implementations
// handle their own concurrency.
func (f *ContextFactory) Snapshot() map[string]any {
	out := make(map[string]any, len(f.gens))
	for k, g := range f.gens {
		out[k] = g.next()
	}
	return out
}

// --- generator implementations ------------------------------------------

// staticGen returns the same value forever. Cheap and lock-free.
type staticGen struct {
	value any
}

func (g staticGen) next() any     { return g.value }
func (g staticGen) isStatic() bool { return true }

// sequenceGen returns start, start+step, start+2*step, ... using a single
// atomic counter shared across all sessions. This is the only correctness-
// critical generator: sequence IDs must be unique.
type sequenceGen struct {
	step    int64
	counter atomic.Int64
}

func (g *sequenceGen) next() any {
	return g.counter.Add(g.step)
}
func (g *sequenceGen) isStatic() bool { return false }

// randomRangeGen returns a uniformly random int64 in [min, max] (inclusive).
type randomRangeGen struct {
	min, max int64
	rng      *lockedRand
}

func (g *randomRangeGen) next() any {
	// rand.Int63n(n) returns [0, n); we shift by min and use (max-min+1) so
	// the bound is inclusive.
	return g.min + g.rng.Int63n(g.max-g.min+1)
}
func (g *randomRangeGen) isStatic() bool { return false }

// randomPickGen returns a random element from a fixed list.
type randomPickGen struct {
	values []string
	rng    *lockedRand
}

func (g *randomPickGen) next() any {
	return g.values[g.rng.Intn(len(g.values))]
}
func (g *randomPickGen) isStatic() bool { return false }

// --- locked rand helper -------------------------------------------------
//
// math/rand's package-level functions take a mutex internally; we use our
// own to avoid contention with unrelated callers and to keep determinism
// hooks available in tests.

type lockedRand struct {
	mu sync.Mutex
	r  *rand.Rand
}

func newLocked() *lockedRand {
	return &lockedRand{r: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func (l *lockedRand) Int63n(n int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Int63n(n)
}

func (l *lockedRand) Intn(n int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Intn(n)
}
