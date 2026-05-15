package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/engine/predicate"
	"livecharge/loadtest/internal/metrics"
	"livecharge/loadtest/internal/template"
	"livecharge/loadtest/internal/transport"
)

// session executes one virtual user's step sequence end-to-end. A new
// session is created (and re-created) by the LoadGenerator for every iteration
// so that .ctx and .session start fresh.
type session struct {
	cfg          *config.ScenarioConfig
	transport    transport.Transport
	factory      *template.ContextFactory
	renderers    map[string]*template.Renderer                    // step name → renderer (built once)
	hdrRenderers map[string]map[string]*template.Renderer         // step → headerName → renderer
	preCalc      map[string][]byte
	stepByName   map[string]int                                   // step name → index into cfg.Steps
	collector    *metrics.Collector
	timeoutAt    func(step config.StepConfig) time.Duration       // resolves response_timeout per step
	exprPrograms map[string][]*predicate.Compiled                 // step name → compiled expr programs
}

// newSession builds the per-session state. Most expensive work (template
// parsing, step-name index) is amortised here so it runs once per session,
// not once per step.
func newSession(
	cfg *config.ScenarioConfig,
	tr transport.Transport,
	factory *template.ContextFactory,
	preCalc map[string][]byte,
	collector *metrics.Collector,
	exprPrograms map[string][]*predicate.Compiled,
) (*session, error) {
	s := &session{
		cfg:          cfg,
		transport:    tr,
		factory:      factory,
		renderers:    make(map[string]*template.Renderer, len(cfg.Steps)),
		hdrRenderers: make(map[string]map[string]*template.Renderer, len(cfg.Steps)),
		preCalc:      preCalc,
		stepByName:   make(map[string]int, len(cfg.Steps)),
		collector:    collector,
		exprPrograms: exprPrograms,
	}
	for i, step := range cfg.Steps {
		s.stepByName[step.Name] = i
		if preCalc[step.Name] == nil {
			r, err := template.NewRenderer(step.Name, step.Template)
			if err != nil {
				return nil, err
			}
			s.renderers[step.Name] = r
		}
		if len(step.Headers) > 0 {
			s.hdrRenderers[step.Name] = make(map[string]*template.Renderer, len(step.Headers))
			for _, h := range step.Headers {
				r, err := template.NewRenderer(step.Name+":"+h.Name, h.Value)
				if err != nil {
					return nil, err
				}
				s.hdrRenderers[step.Name][h.Name] = r
			}
		}
	}
	s.timeoutAt = func(step config.StepConfig) time.Duration {
		if step.ResponseTimeout != nil {
			return step.ResponseTimeout.Duration
		}
		return cfg.Load.ResponseTimeout.Duration
	}
	return s, nil
}

// run drives the step loop for one session, starting at step[0].
// It returns when a predicate selects "" or when an error/timeout ends the run.
func (s *session) run(ctx context.Context) {
	tctx := template.Context{
		"ctx":     s.factory.Snapshot(),
		"session": make(map[string]any, 8),
	}
	// session is the live map we mutate as extracts run; reuse the
	// reference so changes are visible through tctx.
	sessionVals := tctx["session"].(map[string]any)

	stepIdx := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if stepIdx < 0 || stepIdx >= len(s.cfg.Steps) {
			return
		}
		step := s.cfg.Steps[stepIdx]

		body, err := s.renderBody(step, tctx)
		if err != nil {
			s.collector.Submit(metrics.StepResult{Err: err})
			return
		}

		headers, err := s.renderHeaders(step, tctx)
		if err != nil {
			s.collector.Submit(metrics.StepResult{Err: err})
			return
		}

		req := transport.Request{
			Subject:       step.Subject,
			Method:        step.Method,
			Path:          step.Path,
			Headers:       headers,
			Body:          body,
			Timeout:       s.timeoutAt(step),
			FireAndForget: step.FireAndForget,
		}

		s.collector.SubmitSent()
		resp, sendErr := s.transport.Send(ctx, req)

		// Fire-and-forget: no extract/predicate evaluation.
		if step.FireAndForget {
			s.collector.Submit(metrics.StepResult{
				Latency:       resp.Latency,
				Err:           sendErr,
				FireAndForget: true,
			})
			// FAF steps still advance linearly; predicates are disallowed.
			stepIdx++
			continue
		}

		if sendErr != nil {
			s.collector.Submit(metrics.StepResult{Latency: resp.Latency, Err: sendErr})
			return
		}

		// Extracts populate .session for downstream steps and predicates.
		for _, ex := range step.Extracts {
			v, err := template.Extract(resp, ex.Path)
			if err != nil {
				s.collector.Submit(metrics.StepResult{Latency: resp.Latency, Err: err})
				return
			}
			sessionVals[ex.Field] = v
		}

		// Parse the response body so expr predicates can traverse JSON
		// directly via body.*. Non-JSON or parse errors fall back to the raw
		// string — both forms are valid expr inputs.
		var bodyAny any = string(resp.Body)
		if ct, ok := resp.Headers["Content-Type"]; ok && strings.Contains(strings.ToLower(ct), "json") {
			var v any
			if err := json.Unmarshal(resp.Body, &v); err == nil {
				bodyAny = v
			}
		}

		predCtx := predicate.Context{
			Status:  resp.StatusCode,
			Headers: resp.Headers,
			Body:    bodyAny,
			Session: sessionVals,
			Ctx:     tctx["ctx"].(map[string]any),
		}
		result := predicate.Evaluate(step.Predicates, s.exprPrograms[step.Name], predCtx)

		s.collector.Submit(metrics.StepResult{
			Latency:       resp.Latency,
			PredicateName: result.Matched,
		})

		if result.HasMatch {
			if result.NextStep == "" {
				return // explicit end-of-session
			}
			next, ok := s.stepByName[result.NextStep]
			if !ok {
				return // validation should have caught this earlier
			}
			stepIdx = next
			continue
		}

		// No predicate match → fall through to the next step in the list.
		stepIdx++
	}
}

// renderBody returns the request body for this step. Pre-calculated bodies
// are returned as-is; dynamic templates are rendered against tctx.
func (s *session) renderBody(step config.StepConfig, tctx template.Context) ([]byte, error) {
	if b, ok := s.preCalc[step.Name]; ok {
		return b, nil
	}
	r := s.renderers[step.Name]
	return r.Render(tctx)
}

// renderHeaders evaluates each header value as a template. Headers are
// usually short and few, so we don't bother pre-calculating them.
func (s *session) renderHeaders(step config.StepConfig, tctx template.Context) (map[string]string, error) {
	if len(step.Headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(step.Headers))
	rs := s.hdrRenderers[step.Name]
	for _, h := range step.Headers {
		b, err := rs[h.Name].Render(tctx)
		if err != nil {
			return nil, err
		}
		out[h.Name] = string(b)
	}
	return out, nil
}
