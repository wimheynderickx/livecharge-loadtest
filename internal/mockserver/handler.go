package mockserver

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/template"
)

// Handler processes one incoming request for one mock endpoint.
//
// The OK and FAIL templates are pre-compiled at construction time so the
// hot path is just: extract fields → coin flip → Render → return bytes.
// Each handler keeps its own *rand.Rand under a tiny mutex so concurrent
// requests don't fight over the global rand source.
type Handler struct {
	endpoint config.MockEndpointConfig
	okTmpl   *template.Renderer
	failTmpl *template.Renderer
	extracts []template.ExtractField

	mu  sync.Mutex
	rng *rand.Rand
}

// NewHandler pre-compiles the response templates and copies the extract
// configuration into the simpler template.ExtractField form.
func NewHandler(endpoint config.MockEndpointConfig) (*Handler, error) {
	okTmpl, err := template.NewRenderer(endpointLabel(endpoint)+":ok", endpoint.OkResponse)
	if err != nil {
		return nil, fmt.Errorf("mock %s: ok_response: %w", endpointLabel(endpoint), err)
	}

	var failTmpl *template.Renderer
	if endpoint.FailResponse != "" {
		failTmpl, err = template.NewRenderer(endpointLabel(endpoint)+":fail", endpoint.FailResponse)
		if err != nil {
			return nil, fmt.Errorf("mock %s: fail_response: %w", endpointLabel(endpoint), err)
		}
	}

	extracts := make([]template.ExtractField, 0, len(endpoint.Extracts))
	for _, e := range endpoint.Extracts {
		extracts = append(extracts, template.ExtractField{Field: e.Field, Path: e.Path})
	}

	return &Handler{
		endpoint: endpoint,
		okTmpl:   okTmpl,
		failTmpl: failTmpl,
		extracts: extracts,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

// Handle is the per-request entry point.
//
// Decision flow:
//  1. Roll for no_answer_rate. If hit, return noAnswer=true and the caller
//     (NATS subscriber or HTTP handler) skips sending any reply.
//  2. Otherwise extract configured fields from the request body.
//  3. Roll for fail_rate; render the matching ok/fail template.
//
// A returned error means the template failed to render. NATS callers can
// silently drop in that case; HTTP callers should respond 500.
func (h *Handler) Handle(body []byte) (reply []byte, noAnswer bool, err error) {
	if h.shouldDrop() {
		return nil, true, nil
	}

	extracted, exErr := template.ExtractBodyFields(body, h.extracts)
	if exErr != nil {
		// Extraction failure is non-fatal: emit FAIL when configured,
		// otherwise an empty-extracted-map render. The mock keeps
		// replying through bad inputs.
		extracted = map[string]any{}
	}

	if h.shouldFail() && h.failTmpl != nil {
		b, err := h.failTmpl.RenderExtracted(template.NewExtracted(extracted))
		return b, false, err
	}
	b, err := h.okTmpl.RenderExtracted(template.NewExtracted(extracted))
	return b, false, err
}

// shouldDrop returns true with probability endpoint.NoAnswerRate.
// The roll uses the same RNG/mutex as shouldFail.
func (h *Handler) shouldDrop() bool {
	if h.endpoint.NoAnswerRate <= 0 {
		return false
	}
	if h.endpoint.NoAnswerRate >= 1 {
		return true
	}
	h.mu.Lock()
	v := h.rng.Float64()
	h.mu.Unlock()
	return v < h.endpoint.NoAnswerRate
}

// shouldFail returns true with probability endpoint.FailRate.
// FailRate is validated to be in [0, 1] before we get here.
func (h *Handler) shouldFail() bool {
	if h.endpoint.FailRate <= 0 {
		return false
	}
	if h.endpoint.FailRate >= 1 {
		return true
	}
	h.mu.Lock()
	v := h.rng.Float64()
	h.mu.Unlock()
	return v < h.endpoint.FailRate
}

// endpointLabel returns a short identifier for log messages.
// Either the NATS subject or "<METHOD> <path>" for HTTP.
func endpointLabel(ep config.MockEndpointConfig) string {
	if ep.Subject != "" {
		return ep.Subject
	}
	method := ep.Method
	if method == "" {
		method = "POST"
	}
	return method + " " + ep.Path
}
