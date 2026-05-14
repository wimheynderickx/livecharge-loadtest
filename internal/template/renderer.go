package template

import (
	"bytes"
	"fmt"
	"text/template"
)

// Renderer wraps a parsed Go text/template. One Renderer is created per
// template string and reused across all sessions that execute it.
// text/template.Template is safe for concurrent Execute calls, so a single
// Renderer can be shared without further locking.
type Renderer struct {
	name string
	t    *template.Template
}

// NewRenderer parses the supplied template string. name is used in error
// messages to identify which template misbehaved.
func NewRenderer(name, tmpl string) (*Renderer, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}
	return &Renderer{name: name, t: t}, nil
}

// Render executes the template with the given context (a map keyed
// "ctx"/"session") and returns the rendered bytes. The returned slice is
// freshly allocated each call so the caller may retain or mutate it
// without affecting the Renderer.
func (r *Renderer) Render(ctx Context) ([]byte, error) {
	var buf bytes.Buffer
	if err := r.t.Execute(&buf, map[string]any(ctx)); err != nil {
		return nil, fmt.Errorf("render template %q: %w", r.name, err)
	}
	return buf.Bytes(), nil
}

// RenderExtracted is the same as Render but uses the .extracted namespace
// (used by mock-server response templates).
func (r *Renderer) RenderExtracted(ctx Extracted) ([]byte, error) {
	var buf bytes.Buffer
	if err := r.t.Execute(&buf, map[string]any(ctx)); err != nil {
		return nil, fmt.Errorf("render template %q: %w", r.name, err)
	}
	return buf.Bytes(), nil
}
