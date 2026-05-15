package predicate

import "testing"

func TestCompile_TrivialExpression(t *testing.T) {
	prog, err := Compile(`1 == 1`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if prog == nil {
		t.Fatal("Compile returned nil program for valid expression")
	}
}

func TestCompile_SyntaxError(t *testing.T) {
	_, err := Compile(`status == `)
	if err == nil {
		t.Fatal("Compile should error on syntactically broken expression")
	}
}

func TestEval_StatusEquals(t *testing.T) {
	prog := mustCompile(t, `status == 200`)
	got, err := Eval(prog, Context{Status: 200})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal("expected true for status == 200")
	}
}

func TestEval_SessionAndCtx(t *testing.T) {
	prog := mustCompile(t, `session.id == "X" && ctx.amount == "42"`)
	got, err := Eval(prog, Context{
		Session: map[string]any{"id": "X"},
		Ctx:     map[string]any{"amount": "42"},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal("expected true for matching session+ctx")
	}
}

func TestEval_HeadersMap(t *testing.T) {
	prog := mustCompile(t, `headers["Content-Type"] contains "json"`)
	got, err := Eval(prog, Context{
		Headers: map[string]string{"Content-Type": "application/json"},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal(`expected true for "Content-Type" containing "json"`)
	}
}

func TestEval_BodyTreeTraversal(t *testing.T) {
	prog := mustCompile(t, `body.charges[0].status == "OK"`)

	// Caller is responsible for handing us a parsed JSON tree. We just
	// confirm expr-lang can traverse a plain map+slice tree.
	body := map[string]any{
		"charges": []any{
			map[string]any{"status": "OK"},
		},
	}
	got, err := Eval(prog, Context{Body: body})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal("expected true for body.charges[0].status == OK")
	}
}

func TestEval_BodyAsRawString(t *testing.T) {
	prog := mustCompile(t, `body contains "rejected"`)
	got, err := Eval(prog, Context{Body: "request was rejected"})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal(`expected true for body containing "rejected"`)
	}
}

func mustCompile(t *testing.T, src string) *Compiled {
	t.Helper()
	prog, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile %q: %v", src, err)
	}
	return prog
}

func TestEval_MissingVar_StringComparison_False(t *testing.T) {
	prog := mustCompile(t, `session.does_not_exist == "anything"`)
	got, err := Eval(prog, Context{})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got {
		t.Fatal(`expected false: missing string compares unequal to "anything"`)
	}
}

func TestEval_MissingVar_StringEqualsEmpty_True(t *testing.T) {
	prog := mustCompile(t, `session.does_not_exist == ""`)
	got, err := Eval(prog, Context{})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal(`expected true: missing string compares equal to ""`)
	}
}

func TestEval_MissingVar_BooleanLogic(t *testing.T) {
	prog := mustCompile(t, `session.flag != "" && session.other != ""`)
	got, err := Eval(prog, Context{})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got {
		t.Fatal("expected false: missing vars both '', so '!= \"\"' is false on both sides")
	}
}

func TestEval_RuntimeError_ReturnsErrorAndFalse(t *testing.T) {
	// Indexing body[999999] on a map returns an int value (from body.x),
	// then [0] attempts to index that int, which triggers a runtime type error.
	// This expression reliably produces a runtime error from expr.Run without panicking.
	prog := mustCompile(t, `body[999999][0]`)
	body := map[string]any{"x": 1}
	got, err := Eval(prog, Context{Body: body})
	if err == nil {
		t.Fatal("expected runtime error from body[999999][0] indexing")
	}
	if got {
		t.Fatal("expected false return on runtime error")
	}
}
