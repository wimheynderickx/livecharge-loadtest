package engine

import (
	"fmt"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/template"
)

// PreCalcResult holds, for each step, either the pre-rendered body bytes
// (when the template is static) or nil (when it must be rendered per call).
type PreCalcResult struct {
	Bodies map[string][]byte // step name → pre-rendered body, nil if dynamic
}

// PreCalc inspects every step template and pre-renders the ones that can be
// reused across sessions. This avoids running text/template through the hot
// path for fully static payloads at >100k msg/sec.
//
// A template qualifies as static when it references neither .session.X nor
// any .ctx.X variable backed by a generator (sequence / random_*).
func PreCalc(cfg *config.ScenarioConfig, factory *template.ContextFactory) (PreCalcResult, error) {
	defs := factory.Definitions()
	bodies := make(map[string][]byte, len(cfg.Steps))

	// Build a Context whose "ctx" map is the factory's first snapshot.
	// For static templates the values are scalar generators, so any
	// snapshot produces the same result. We also include an empty
	// "session" map so templates that reference {{.session.X}} fail at
	// the static check rather than rendering empty strings here.
	staticCtx := template.Context{
		"ctx":     factory.Snapshot(),
		"session": map[string]any{},
	}

	for _, step := range cfg.Steps {
		if !template.IsStatic(step.Template, defs) {
			continue
		}
		r, err := template.NewRenderer(step.Name, step.Template)
		if err != nil {
			return PreCalcResult{}, fmt.Errorf("step %q: %w", step.Name, err)
		}
		body, err := r.Render(staticCtx)
		if err != nil {
			return PreCalcResult{}, fmt.Errorf("step %q: pre-render: %w", step.Name, err)
		}
		bodies[step.Name] = body
	}
	return PreCalcResult{Bodies: bodies}, nil
}
