package engine

import (
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"livecharge/loadtest/internal/config"
)

// TestRunner_NaturalCompletion_NoContextCancelled is a regression test for
// the bug where hitting total_messages with concurrency>1 would cancel the
// shared context, aborting in-flight requests in sibling workers with
// "context canceled".
//
// We spin up an httptest server that adds a small delay so several
// requests are in flight when the total-messages limit is reached, run a
// real scenario through the engine, and assert that the final error count
// is zero.
func TestRunner_NaturalCompletion_NoContextCancelled(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		hits.Add(1)
		// Delay so multiple workers are in flight simultaneously.
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.toml")
	tomlBody := fmt.Sprintf(`
[scenario]
name = "regression"

[transport]
type = "http"
url  = %q

[transport.auth]
type = "none"

[load]
rate             = 0
total_messages   = 50
concurrency      = 10
response_timeout = "5s"

[metrics]
percentiles = [50, 95, 99]

[[step]]
name     = "ping"
method   = "GET"
path     = "/"
template = "{}"
`, srv.URL)
	if err := os.WriteFile(path, []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.LoadScenario(path)
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}

	r, err := NewRunner(loaded)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer r.Close()

	if err := r.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for natural completion, with a generous timeout for slow CI.
	select {
	case <-r.DoneCh():
	case <-time.After(10 * time.Second):
		t.Fatal("scenario did not finish within 10s")
	}

	snap := r.Snapshot()
	if snap.Errors != 0 {
		t.Errorf("expected 0 errors after natural completion, got %d (sent=%d received=%d)",
			snap.Errors, snap.Sent, snap.Received)
	}
	if snap.Sent != 50 {
		t.Errorf("expected exactly 50 sends, got %d", snap.Sent)
	}
	if snap.Received != 50 {
		t.Errorf("expected all 50 to receive, got %d", snap.Received)
	}
	// Sanity: hits at the server should match sends — proves no requests
	// were aborted mid-flight.
	if got := hits.Load(); got != 50 {
		t.Errorf("expected 50 hits at the test server, got %d", got)
	}
	if strings.Contains(snap.StateName, "ERROR") {
		t.Errorf("scenario state should not be ERROR, got %s", snap.StateName)
	}
}


// TestRunner_OnStartFiresOnce verifies that OnStart fires when a runner
// transitions IDLE → RUNNING from a fresh Start, and that the callback is
// NOT triggered by Resume (which continues a prior run). OnTerminal must
// fire after natural completion so the post-run email pipeline sees one
// "start" → one "done" cycle even when the user pauses + resumes mid-run.
func TestRunner_OnStartFiresOnce(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.toml")
	tomlBody := fmt.Sprintf(`
[scenario]
name = "onstart"

[transport]
type = "http"
url  = %q

[transport.auth]
type = "none"

[load]
rate             = 0
total_messages   = 5
concurrency      = 1
response_timeout = "5s"

[metrics]
percentiles = [50]

[[step]]
name     = "ping"
method   = "GET"
path     = "/"
template = "{}"
`, srv.URL)
	if err := os.WriteFile(path, []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadScenario(path)
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	r, err := NewRunner(loaded)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer r.Close()

	var startCalls atomic.Int32
	var terminalCalls atomic.Int32
	r.SetOnStart(func() { startCalls.Add(1) })
	r.SetOnTerminal(func(State) { terminalCalls.Add(1) })

	if err := r.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-r.DoneCh():
	case <-time.After(5 * time.Second):
		t.Fatal("scenario did not finish in time")
	}

	// OnStart is fired in a goroutine — give it a moment to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	for startCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := startCalls.Load(); got != 1 {
		t.Errorf("expected OnStart to fire exactly once, got %d", got)
	}
	if got := terminalCalls.Load(); got != 1 {
		t.Errorf("expected OnTerminal to fire exactly once, got %d", got)
	}
}

// TestRunner_OnStartSkipsResume verifies that Resume() — continuing a
// stopped scenario — does NOT re-fire OnStart. Email-feature semantics
// rely on "start" emails happening exactly once per fresh run.
func TestRunner_OnStartSkipsResume(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		// Slow handler so we can Stop while requests are in flight.
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.toml")
	tomlBody := fmt.Sprintf(`
[scenario]
name = "resume-test"

[transport]
type = "http"
url  = %q

[transport.auth]
type = "none"

[load]
rate             = 0
total_messages   = 0
duration         = "10s"
concurrency      = 1
response_timeout = "5s"

[metrics]
percentiles = [50]

[[step]]
name     = "ping"
method   = "GET"
path     = "/"
template = "{}"
`, srv.URL)
	if err := os.WriteFile(path, []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadScenario(path)
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	r, err := NewRunner(loaded)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer r.Close()

	var startCalls atomic.Int32
	r.SetOnStart(func() { startCalls.Add(1) })

	if err := r.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Wait until the OnStart handler has actually fired before stopping,
	// rather than relying on a fixed sleep.
	deadline := time.Now().Add(2 * time.Second)
	for startCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := startCalls.Load(); got != 1 {
		t.Fatalf("initial Start should fire OnStart once, got %d", got)
	}

	if err := r.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Let the background watcher fully drain before Resume — without
	// this we hit a pre-existing engine race in load_generator's
	// WaitGroup that isn't related to OnStart semantics. The watcher
	// runs in its own goroutine and may still be executing past
	// generator.Wait() when Stop() returns.
	time.Sleep(300 * time.Millisecond)

	// Resume should NOT re-fire OnStart.
	if err := r.Resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	_ = r.Stop()
	time.Sleep(150 * time.Millisecond)

	if got := startCalls.Load(); got != 1 {
		t.Errorf("OnStart should fire only on fresh Start, got %d calls (Resume should NOT fire it)", got)
	}
}

func TestStateScriptError_String(t *testing.T) {
	if got := StateScriptError.String(); got != "SCRIPT_ERROR" {
		t.Fatalf("StateScriptError.String() = %q, want %q", got, "SCRIPT_ERROR")
	}
}
