package main

import (
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/engine"
)

// TestExprPredicate_EndToEnd drives a scenario that uses op=expr against
// body.status and confirms predicate counters increment correctly.
func TestExprPredicate_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/charges" {
			_, _ = w.Write([]byte(`{"id":"CHG-1","status":"created"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "s.toml")
	body := []byte(`
[scenario]
name = "expr-e2e"
[transport]
type = "http"
url  = "` + srv.URL + `"
[transport.auth]
type = "none"
[load]
rate = 50
total_messages = 10
concurrency = 2
response_timeout = "2s"
[metrics]
percentiles = [50]
[[step]]
name = "create"
method = "POST"
path = "/v1/charges"
template = "{}"
[[step.predicate]]
name = "created"
op = "expr"
value = 'body.status == "created"'
next_step = ""
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.LoadScenario(path)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	r, err := engine.NewRunner(loaded)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-r.DoneCh():
	case <-time.After(10 * time.Second):
		t.Fatal("scenario did not finish")
	}

	snap := r.Snapshot()
	pred, ok := snap.Predicates["created"]
	if !ok {
		t.Fatalf(`predicate "created" missing from snapshot; got: %v`, snap.Predicates)
	}
	if pred.Count != 10 {
		t.Fatalf(`predicate "created" count = %d, want 10`, pred.Count)
	}
}
