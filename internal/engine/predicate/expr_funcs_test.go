package predicate

import (
	"os"
	"testing"
)

func TestToInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{"42", 42},
		{"", 0},
		{"abc", 0},
		{int(7), 7},
		{float64(3.9), 3},
		{nil, 0},
	}
	for _, c := range cases {
		if got := toInt(c.in); got != c.want {
			t.Errorf("toInt(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestToFloat(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{"3.14", 3.14},
		{"", 0},
		{"nope", 0},
		{int(2), 2.0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := toFloat(c.in); got != c.want {
			t.Errorf("toFloat(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{42, "42"},
		{"already", "already"},
		{nil, ""},
		{3.5, "3.5"},
	}
	for _, c := range cases {
		if got := toString(c.in); got != c.want {
			t.Errorf("toString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("LOADTEST_TEST_VAR", "hello")
	if got := envFunc("LOADTEST_TEST_VAR"); got != "hello" {
		t.Errorf("env() = %q, want %q", got, "hello")
	}
	os.Unsetenv("LOADTEST_TEST_VAR_NOT_SET")
	if got := envFunc("LOADTEST_TEST_VAR_NOT_SET"); got != "" {
		t.Errorf("env(unset) = %q, want \"\"", got)
	}
}

func TestEval_WithToInt(t *testing.T) {
	prog := mustCompile(t, `toInt(session.amount) > 10`)
	got, err := Eval(prog, Context{Session: map[string]any{"amount": "42"}})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !got {
		t.Fatal("expected true: 42 > 10")
	}
}
