package predicate

import (
	"testing"

	"livecharge/loadtest/internal/config"
)

func BenchmarkEval_ClassicEq(b *testing.B) {
	preds := []config.PredicateConfig{
		{Name: "p", Op: "eq", Field: "status", Value: "200"},
	}
	ctx := Context{
		Status:  200,
		Session: map[string]any{},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Evaluate(preds, nil, ctx)
	}
}

func BenchmarkEval_Expr_StatusEq(b *testing.B) {
	prog, err := Compile(`status == 200`)
	if err != nil {
		b.Fatal(err)
	}
	preds := []config.PredicateConfig{
		{Name: "p", Op: "expr", Value: `status == 200`},
	}
	compiled := []*Compiled{prog}
	ctx := Context{Status: 200, Session: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Evaluate(preds, compiled, ctx)
	}
}

func BenchmarkCompile(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = Compile(`status == 200 && body.amount == ctx.amount`)
	}
}
