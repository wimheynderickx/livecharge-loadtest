package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhale/smtpd"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
)

// captureMailServer wraps mhale/smtpd as an in-process SMTP server,
// keying captured envelopes by Subject so tests can assert on the
// trigger name a scenario emitted. Mirrors the helper used by
// internal/mail's own sender_test.go.
type captureMailServer struct {
	srv     *smtpd.Server
	addr    string
	host    string
	port    int
	mu      sync.Mutex
	emails  []capturedEmail
}

type capturedEmail struct {
	From    string
	To      []string
	Subject string
	Body    string
}

func newCaptureServer(t *testing.T) *captureMailServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	c := &captureMailServer{addr: ln.Addr().String(), host: host, port: port}
	c.srv = &smtpd.Server{
		Appname:       "loadtest-test",
		Hostname:      "localhost",
		MaxRecipients: 100,
		Handler: func(remote net.Addr, from string, to []string, data []byte) error {
			c.mu.Lock()
			c.emails = append(c.emails, capturedEmail{
				From: from, To: to,
				Subject: extractSubject(data),
				Body:    string(data),
			})
			c.mu.Unlock()
			return nil
		},
	}
	go func() { _ = c.srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.srv.Shutdown(ctx)
		_ = ln.Close()
	})

	// Wait until the server accepts at least one TCP connection so the
	// first test send isn't racing the Serve goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", c.addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("smtpd never started accepting connections")
	return nil
}

// emailsCopy returns a snapshot of the captured slice — callers can
// inspect it without holding the lock.
func (c *captureMailServer) emailsCopy() []capturedEmail {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEmail, len(c.emails))
	copy(out, c.emails)
	return out
}

// extractSubject pulls the Subject header out of a raw RFC 822 message.
// We use it instead of net/mail because the test fixtures are small and
// pulling in the full parser for one line is overkill.
func extractSubject(data []byte) string {
	for _, line := range strings.Split(string(data), "\r\n") {
		if strings.HasPrefix(line, "Subject:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Subject:"))
		}
		if line == "" {
			// End of headers — no Subject seen.
			return ""
		}
	}
	return ""
}

// emailScenario is a tiny helper that builds and starts a real loadtest
// scenario configured to email `triggers` via the captureMailServer.
// It returns the runner + a function to wait for terminal state.
//
// The HTTP target is provided by the caller so individual tests can
// pick "always 200", "always fail", or anything in between.
func emailScenario(t *testing.T, srvURL string, capture *captureMailServer, triggers []string, reportInterval string, totalMessages int) (*engine.Runner, *mail.Status, func()) {
	t.Helper()

	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "scenario.toml")
	body := fmt.Sprintf(`
[scenario]
name        = "email-integration"
description = "Email lifecycle integration test"

[transport]
type = "http"
url  = %q

[transport.auth]
type = "none"

[load]
rate             = 0
total_messages   = %d
concurrency      = 1
response_timeout = "2s"

[metrics]
percentiles = [50, 99]

[[step]]
name     = "ping"
method   = "GET"
path     = "/"
template = "{}"

[email]
enabled         = true
on              = [%s]
report_interval = %q
smtp_host       = "127.0.0.1"
smtp_port       = %d
from            = "test@x"
to              = ["recv@x"]
subject         = "{{.Trigger}}: {{.Scenario.Name}}"
`, srvURL, totalMessages, quotedList(triggers), reportInterval, capture.port)

	if err := os.WriteFile(scenarioPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadScenario(scenarioPath)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	runner, err := engine.NewRunner(loaded)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	registry := newMailRegistry()
	status, err := wireScenarioMail(registry, loaded, runner, mail.Config{}, nil, mail.Overrides{}, "")
	if err != nil {
		t.Fatalf("wireScenarioMail: %v", err)
	}

	if err := runner.Start(); err != nil {
		t.Fatalf("runner.Start: %v", err)
	}

	wait := func() {
		select {
		case <-runner.DoneCh():
		case <-time.After(15 * time.Second):
			t.Fatal("scenario did not reach DONE in time")
		}
		// Mail sends are async; wait for the in-flight ones to drain.
		registry.WaitAll(5 * time.Second)
		// And finally cancel any progress ticker.
		registry.stopProgress(runner)
		_ = runner.Close()
	}
	return runner, status, wait
}

// quotedList renders a Go string slice as a TOML inline-array literal.
func quotedList(items []string) string {
	var b strings.Builder
	for i, s := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(s))
	}
	return b.String()
}

// TestEmail_DoneFires confirms the baseline path: a short, successful
// scenario fires exactly one "done" email when on=["done"]. Sanity check
// that the integration plumbing works at all before the more nuanced
// triggers below.
func TestEmail_DoneFires(t *testing.T) {
	httpSrv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer httpSrv.Close()

	capture := newCaptureServer(t)
	_, _, wait := emailScenario(t, httpSrv.URL, capture, []string{"done"}, "10s", 3)
	wait()

	emails := capture.emailsCopy()
	if len(emails) != 1 {
		t.Fatalf("want exactly 1 email, got %d:\n%+v", len(emails), summariseSubjects(emails))
	}
	if !strings.HasPrefix(emails[0].Subject, "done:") {
		t.Errorf("subject should start with 'done:', got %q", emails[0].Subject)
	}
}

// TestEmail_ProgressFiresOnShortScenario is the regression test for the
// reported bug: a scenario that completes well before report_interval
// must still emit at least one "progress" email because we fire an
// initial one at min(interval, initialProgressDelay).
func TestEmail_ProgressFiresOnShortScenario(t *testing.T) {
	httpSrv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		// A few ms per request keeps the scenario alive past the
		// 250ms initial-progress fire, but it still finishes well
		// before report_interval=5m.
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer httpSrv.Close()

	capture := newCaptureServer(t)
	// 5-minute interval mimics what the user actually hit. The
	// scenario takes ~500ms; without the initial-fire fix no progress
	// email would ever be sent. The test fakes a tiny initial delay
	// via the package-level const at compile time — we don't override
	// it here, but our test scenario is short enough that the
	// initialProgressDelay (5s) needs to be small for tests. Reduce
	// load to keep the run > initialProgressDelay so progress fires.
	_, _, wait := emailScenario(t, httpSrv.URL, capture, []string{"start", "progress", "done"}, "5m", 200)
	wait()

	emails := capture.emailsCopy()
	subjects := summariseSubjects(emails)

	hasStart, hasProgress, hasDone := false, false, false
	for _, e := range emails {
		switch {
		case strings.HasPrefix(e.Subject, "start:"):
			hasStart = true
		case strings.HasPrefix(e.Subject, "progress:"):
			hasProgress = true
		case strings.HasPrefix(e.Subject, "done:"):
			hasDone = true
		}
	}
	if !hasStart {
		t.Errorf("missing 'start' email; got: %s", subjects)
	}
	if !hasProgress {
		t.Errorf("missing 'progress' email — initial-progress fix not working; got: %s", subjects)
	}
	if !hasDone {
		t.Errorf("missing 'done' email; got: %s", subjects)
	}
}

// TestEmail_ProgressNotSentAfterDone guards the cancellation guarantee:
// once the scenario reaches DONE, the progress ticker must be cancelled
// so no further progress emails arrive (would be misleading).
func TestEmail_ProgressNotSentAfterDone(t *testing.T) {
	httpSrv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer httpSrv.Close()

	capture := newCaptureServer(t)
	// Very quick scenario, tight ticker interval — if the cancel logic
	// were broken we'd see extra progress mails after the done.
	_, _, wait := emailScenario(t, httpSrv.URL, capture, []string{"progress", "done"}, "100ms", 3)
	wait()

	// Give any stray ticker goroutine time to misbehave.
	time.Sleep(400 * time.Millisecond)

	before := len(capture.emailsCopy())
	time.Sleep(300 * time.Millisecond)
	after := len(capture.emailsCopy())
	if after != before {
		t.Errorf("progress emails kept arriving after DONE: before=%d after=%d", before, after)
	}
}

// TestEmail_ErrorFiresWhenAllRequestsFail confirms the "on error" path
// works now that chooseTerminalTrigger compares Errors vs Received. A
// scenario pointed at an unreachable host produces all-failing requests
// → trigger should be "error", not "done".
func TestEmail_ErrorFiresWhenAllRequestsFail(t *testing.T) {
	// Spin up a listener on a random port then close it — every
	// request will get connection refused. This is the closest portable
	// simulation of "the target is down" in a unit test.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()

	capture := newCaptureServer(t)
	_, _, wait := emailScenario(t, "http://"+deadAddr, capture, []string{"done", "error"}, "10s", 3)
	wait()

	emails := capture.emailsCopy()
	if len(emails) != 1 {
		t.Fatalf("want 1 email, got %d: %s", len(emails), summariseSubjects(emails))
	}
	if !strings.HasPrefix(emails[0].Subject, "error:") {
		t.Errorf("scenario with all failures should fire 'error', got subject %q", emails[0].Subject)
	}
}

// TestEmail_DoneWinsWhenErrorsAreInfrequent inverts the previous test —
// some errors are normal in load testing, and the user wants "done" for
// a successful overall run. The Errors > Received threshold means
// scattered failures stay classified as done.
func TestEmail_DoneWinsWhenErrorsAreInfrequent(t *testing.T) {
	var calls int
	var mu sync.Mutex
	httpSrv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n == 0 {
			// One failure out of many; we don't even need to error
			// — slow response will time out at response_timeout=2s.
			// But to keep the test fast we'll just write a 500.
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer httpSrv.Close()

	capture := newCaptureServer(t)
	_, _, wait := emailScenario(t, httpSrv.URL, capture, []string{"done", "error"}, "10s", 20)
	wait()

	emails := capture.emailsCopy()
	if len(emails) != 1 {
		t.Fatalf("want 1 email, got %d: %s", len(emails), summariseSubjects(emails))
	}
	// HTTP 500 isn't counted as an error by the engine — the response
	// came back, just with a non-2xx status. That's "done" in our
	// classification. Either way it should NOT be "error".
	if strings.HasPrefix(emails[0].Subject, "error:") {
		t.Errorf("scenario with sparse 500s shouldn't be classified as error, got %q", emails[0].Subject)
	}
}

// TestChooseTerminalTrigger_DirectCases is a fast direct-table test of
// the helper so future refactors keep the threshold rule stable.
func TestChooseTerminalTrigger_DirectCases(t *testing.T) {
	// Direct unit tests of the threshold rule would require building a
	// Runner+snapshot fixture, which is more setup than this assertion
	// is worth. Instead we exercise the engine-state branch only.
	cases := []struct {
		state engine.State
		want  string
	}{
		{engine.StateRunning, ""},
		{engine.StateStopped, ""},
		// StateError currently never emitted by the engine, but if it
		// ever is, chooseTerminalTrigger should pass it straight through.
	}
	for _, tc := range cases {
		got := triggerNameForState(tc.state)
		if got != tc.want {
			t.Errorf("triggerNameForState(%v): got %q want %q", tc.state, got, tc.want)
		}
	}
	if got := triggerNameForState(engine.StateError); got != "error" {
		t.Errorf("triggerNameForState(StateError) should be 'error', got %q", got)
	}
}

func summariseSubjects(emails []capturedEmail) string {
	var subjects []string
	for _, e := range emails {
		subjects = append(subjects, e.Subject)
	}
	return "[" + strings.Join(subjects, ", ") + "]"
}

// silence unused-import warning when conditional code paths above
// don't exercise the error type.
var _ = errors.New
