package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/engine"
)

// TestExprPredicate_BadSiblingDoesNotKillRun confirms that a script error
// in one scenario doesn't prevent a healthy sibling scenario from running.
func TestExprPredicate_BadSiblingDoesNotKillRun(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.toml")
	good := filepath.Join(dir, "good.toml")

	if err := os.WriteFile(bad, []byte(`
[scenario]
name = "bad"
[transport]
type = "http"
url  = "http://localhost:1"
[transport.auth]
type = "none"
[load]
rate = 1
total_messages = 1
concurrency = 1
response_timeout = "100ms"
[metrics]
percentiles = [50]
[[step]]
name = "s"
method = "GET"
path = "/"
template = "{}"
[[step.predicate]]
name  = "p"
op    = "expr"
value = "status =="
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(good, []byte(`
[scenario]
name = "good"
[transport]
type = "http"
url  = "http://localhost:1"
[transport.auth]
type = "none"
[load]
rate = 1
total_messages = 1
concurrency = 1
response_timeout = "100ms"
[metrics]
percentiles = [50]
[[step]]
name = "s"
method = "GET"
path = "/"
template = "{}"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	badLoaded, err := config.LoadScenario(bad)
	if err != nil {
		t.Fatalf("load bad: %v", err)
	}
	goodLoaded, err := config.LoadScenario(good)
	if err != nil {
		t.Fatalf("load good: %v", err)
	}

	badRunner, err := engine.NewRunner(badLoaded)
	if err != nil {
		t.Fatalf("NewRunner(bad): %v", err)
	}
	goodRunner, err := engine.NewRunner(goodLoaded)
	if err != nil {
		t.Fatalf("NewRunner(good): %v", err)
	}

	if badRunner.State() != engine.StateScriptError {
		t.Fatalf("bad runner state = %s, want SCRIPT_ERROR", badRunner.State())
	}
	if badRunner.ScriptError() == "" {
		t.Fatal("bad runner ScriptError() should be populated")
	}

	// Good runner starts normally.
	if err := goodRunner.Start(); err != nil {
		t.Fatalf("good Start: %v", err)
	}
	select {
	case <-goodRunner.DoneCh():
	case <-time.After(2 * time.Second):
		t.Fatal("good runner did not finish in time")
	}
}
