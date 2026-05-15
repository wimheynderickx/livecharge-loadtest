package predicate

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

// Compiled wraps an expr-lang program plus the source string and the
// set of namespace keys the source references. The keys are used at
// Eval time to pre-fill missing entries with "" so traversal never
// sees nil — the permissive missing-variable rule documented in the spec.
type Compiled struct {
	Source      string
	Program     *vm.Program
	SessionKeys []string
	CtxKeys     []string
	HeaderKeys  []string
}

// Compile parses and type-checks src. It enables expr-lang's
// AllowUndefinedVariables so a reference like body.does.not.exist is a
// compile-time pass (it becomes a runtime resolver call).
//
// The returned program is safe for concurrent Eval calls.
func Compile(src string) (*Compiled, error) {
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("compile %q: %w", src, err)
	}
	c := &Compiled{Source: src}
	ast.Walk(&tree.Node, &refCollector{c: c})

	prog, err := expr.Compile(src,
		expr.AllowUndefinedVariables(),
		expr.AsBool(),
		expr.Function("toInt", func(params ...any) (any, error) { return toInt(params[0]), nil }),
		expr.Function("toFloat", func(params ...any) (any, error) { return toFloat(params[0]), nil }),
		expr.Function("toString", func(params ...any) (any, error) { return toString(params[0]), nil }),
		expr.Function("env", func(params ...any) (any, error) {
			s, _ := params[0].(string)
			return envFunc(s), nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("compile %q: %w", src, err)
	}
	c.Program = prog
	return c, nil
}

// refCollector walks the AST and records the literal keys accessed
// under session, ctx, and headers — i.e. `session.X` and `session["X"]`
// forms. The collected sets feed Eval's zero-fill so missing keys
// resolve to "" instead of nil.
type refCollector struct {
	c *Compiled
}

func (v *refCollector) Visit(node *ast.Node) {
	mem, ok := (*node).(*ast.MemberNode)
	if !ok {
		return
	}
	ident, ok := mem.Node.(*ast.IdentifierNode)
	if !ok {
		return
	}
	key, ok := stringPropertyOrIndex(mem)
	if !ok {
		return
	}
	switch ident.Value {
	case "session":
		v.c.SessionKeys = append(v.c.SessionKeys, key)
	case "ctx":
		v.c.CtxKeys = append(v.c.CtxKeys, key)
	case "headers":
		v.c.HeaderKeys = append(v.c.HeaderKeys, key)
	}
}

// stringPropertyOrIndex extracts the literal key from a MemberNode.
// Returns ("", false) if the property is not a literal — e.g. a dynamic
// expression like `headers[someVar]` — because we can't pre-fill what
// we can't see at compile time.
func stringPropertyOrIndex(mem *ast.MemberNode) (string, bool) {
	if mem.Property == nil {
		return "", false
	}
	switch p := mem.Property.(type) {
	case *ast.StringNode:
		return p.Value, true
	case *ast.IdentifierNode:
		return p.Value, true
	}
	return "", false
}

// Context is the per-evaluation input. Body is opaque to this package
// (the caller supplies the parsed JSON tree or a raw string).
type Context struct {
	Status  int
	Headers map[string]string
	Body    any
	Session map[string]any
	Ctx     map[string]any
}

// asEnv builds the expr environment map. Missing maps materialise as
// empty maps so expressions that touch them never panic on nil.
func (c Context) asEnv() map[string]any {
	headers := c.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	session := c.Session
	if session == nil {
		session = map[string]any{}
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = map[string]any{}
	}
	return map[string]any{
		"status":  c.Status,
		"headers": headers,
		"body":    c.Body,
		"session": session,
		"ctx":     ctx,
	}
}

// Eval runs prog against ctx and returns the boolean result. expr-lang's
// AsBool() option ensures non-boolean results error out at compile time,
// so a successful return here is always a valid bool.
//
// Runtime errors (type mismatches, nil-pointer traversals not absorbed
// by the resolver) bubble up — callers decide whether to treat them as
// "no match" or surface them.
func Eval(prog *Compiled, ctx Context) (bool, error) {
	env := ctx.asEnv()

	// Clone the namespace maps and pre-fill with "" for any key the
	// compiler saw but the runtime map is missing. Cloning keeps the
	// caller's map free of accidental mutation under concurrent eval.
	env["session"] = withZeroes(asAnyMap(env["session"]), prog.SessionKeys)
	env["ctx"] = withZeroes(asAnyMap(env["ctx"]), prog.CtxKeys)
	env["headers"] = withZeroesString(asStringMap(env["headers"]), prog.HeaderKeys)
	env["toInt"] = func(v any) int { return toInt(v) }
	env["toFloat"] = func(v any) float64 { return toFloat(v) }
	env["toString"] = func(v any) string { return toString(v) }
	env["env"] = func(s string) string { return envFunc(s) }

	out, err := expr.Run(prog.Program, env)
	if err != nil {
		return false, err
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("expr %q returned non-bool %T", prog.Source, out)
	}
	return b, nil
}

func asAnyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asStringMap(v any) map[string]string {
	if m, ok := v.(map[string]string); ok {
		return m
	}
	return map[string]string{}
}

func withZeroes(src map[string]any, keys []string) map[string]any {
	out := make(map[string]any, len(src)+len(keys))
	for k, v := range src {
		out[k] = v
	}
	for _, k := range keys {
		if _, ok := out[k]; !ok {
			out[k] = ""
		}
	}
	return out
}

func withZeroesString(src map[string]string, keys []string) map[string]string {
	out := make(map[string]string, len(src)+len(keys))
	for k, v := range src {
		out[k] = v
	}
	for _, k := range keys {
		if _, ok := out[k]; !ok {
			out[k] = ""
		}
	}
	return out
}
