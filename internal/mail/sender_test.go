package mail

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhale/smtpd"
)

// mockServer wraps github.com/mhale/smtpd as an in-process SMTP server
// listening on a random local port. Tests start one with newMockServer,
// send mail through Sender pointing at its address, then assert against
// the captured envelopes.
//
// We use mhale/smtpd (not a hand-rolled responder) so that authentication
// negotiation and AUTH PLAIN/LOGIN are exercised against a real codepath
// — that's where most sender bugs hide.
type mockServer struct {
	srv     *smtpd.Server
	addr    string
	host    string
	port    int
	mu      sync.Mutex
	deliveries []delivery
	auths      []authAttempt
}

type delivery struct {
	From string
	To   []string
	Data []byte
}

type authAttempt struct {
	Mechanism string
	Username  string
	Password  string
}

// newMockServer starts a fresh server. authRequired toggles whether the
// server demands credentials. Tests that want to assert on AUTH look at
// the Auths slice; everyone else uses Deliveries.
func newMockServer(t *testing.T, authRequired bool) *mockServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	m := &mockServer{addr: ln.Addr().String(), host: host, port: port}
	m.srv = &smtpd.Server{
		Appname:       "loadtest-test",
		Hostname:      "localhost",
		AuthMechs:     map[string]bool{"PLAIN": true, "LOGIN": true},
		AuthRequired:  authRequired,
		MaxRecipients: 100,
		Handler: func(remoteAddr net.Addr, from string, to []string, data []byte) error {
			m.mu.Lock()
			m.deliveries = append(m.deliveries, delivery{From: from, To: to, Data: data})
			m.mu.Unlock()
			return nil
		},
		AuthHandler: func(remoteAddr net.Addr, mechanism string, username, password, shared []byte) (bool, error) {
			m.mu.Lock()
			m.auths = append(m.auths, authAttempt{
				Mechanism: mechanism,
				Username:  string(username),
				Password:  string(password),
			})
			m.mu.Unlock()
			// Accept any non-empty user+pass so we can assert on values
			// without baking secrets into the test fixtures.
			if len(username) == 0 || len(password) == 0 {
				return false, errors.New("empty credentials")
			}
			return true, nil
		},
	}

	go func() {
		_ = m.srv.Serve(ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.srv.Shutdown(ctx)
		_ = ln.Close()
	})

	// Wait for the server to be reachable before returning. Without this,
	// the first test send sometimes races the goroutine that calls Serve.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", m.addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return m
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("mock SMTP server never started accepting connections")
	return nil
}

// Deliveries returns a copy of the captured deliveries.
func (m *mockServer) Deliveries() []delivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]delivery, len(m.deliveries))
	copy(cp, m.deliveries)
	return cp
}

// Auths returns a copy of the captured auth attempts.
func (m *mockServer) Auths() []authAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]authAttempt, len(m.auths))
	copy(cp, m.auths)
	return cp
}

// senderFor returns a Sender configured to dial this mock server. We have
// to inject the dialer because the production code derives the address
// from SMTPHost:SMTPPort — passing "127.0.0.1" as SMTPHost is enough but
// being explicit about the dialer makes the test bulletproof against
// future code that adds e.g. SRV resolution.
func (m *mockServer) senderFor(cfg Config) *Sender {
	cfg.SMTPHost = m.host
	cfg.SMTPPort = m.port
	s := NewSender(cfg)
	s.Dialer = func(network, addr string) (net.Conn, error) {
		return net.Dial(network, m.addr)
	}
	return s
}

func TestSender_PlainSendNoAuth(t *testing.T) {
	srv := newMockServer(t, false)
	sender := srv.senderFor(Config{Enabled: true, From: "alice@example.com", To: []string{"bob@example.com"}})

	msg := Message{
		From:    "alice@example.com",
		To:      []string{"bob@example.com"},
		Subject: "hello",
		TextBody: "world",
	}
	status := &Status{}
	<-sender.SendAsync(msg, status)

	if got := status.State(); got != StateSent {
		t.Fatalf("status: %s err=%v", got, status.Snapshot().Err)
	}
	d := srv.Deliveries()
	if len(d) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(d))
	}
	if d[0].From != "alice@example.com" {
		t.Errorf("From envelope: %q", d[0].From)
	}
	if len(d[0].To) != 1 || d[0].To[0] != "bob@example.com" {
		t.Errorf("To envelope: %v", d[0].To)
	}
	if !strings.Contains(string(d[0].Data), "Subject: hello") {
		t.Errorf("Subject header missing from body: %s", d[0].Data)
	}
	if !strings.Contains(string(d[0].Data), "world") {
		t.Errorf("Body missing: %s", d[0].Data)
	}
}

func TestSender_PlainAuth(t *testing.T) {
	srv := newMockServer(t, true)
	sender := srv.senderFor(Config{
		Enabled: true,
		From:    "a@b", To: []string{"c@d"},
		SMTPUser: "smtpuser",
		SMTPPass: "smtppass",
	})

	<-sender.SendAsync(Message{From: "a@b", To: []string{"c@d"}, TextBody: "hi"}, &Status{})

	auths := srv.Auths()
	if len(auths) == 0 {
		t.Fatal("expected at least one auth attempt")
	}
	last := auths[len(auths)-1]
	if last.Username != "smtpuser" || last.Password != "smtppass" {
		t.Fatalf("auth credentials wrong: %+v", last)
	}
	if last.Mechanism != "PLAIN" {
		t.Errorf("expected PLAIN mechanism (since we're loopback we don't STARTTLS), got %s", last.Mechanism)
	}
}

func TestSender_RecordsAuthFailure(t *testing.T) {
	// Empty creds make the handler reject. The error should surface in
	// Status.MarkFailed rather than panicking.
	srv := newMockServer(t, true)
	sender := srv.senderFor(Config{
		Enabled:  true,
		From:     "a@b", To: []string{"c@d"},
		SMTPUser: "x",
		SMTPPass: "",
	})

	status := &Status{}
	<-sender.SendAsync(Message{From: "a@b", To: []string{"c@d"}}, status)

	// With empty pass our sender's wantAuth check (which requires BOTH user
	// and pass non-empty) skips the auth step. The server's AuthRequired
	// then refuses MAIL FROM. We just want a Failed status either way.
	snap := status.Snapshot()
	if snap.State != StateFailed {
		t.Fatalf("expected StateFailed, got %s (err=%v)", snap.State, snap.Err)
	}
}

func TestSender_Attachments(t *testing.T) {
	srv := newMockServer(t, false)
	sender := srv.senderFor(Config{Enabled: true, From: "a@b", To: []string{"c@d"}})

	msg := Message{
		From: "a@b", To: []string{"c@d"}, Subject: "s",
		TextBody: "body text",
		Attachments: []Attachment{
			{Name: "scenario.log", Content: []byte("LOG LINE 1\nLOG LINE 2\n")},
			{Name: "run.log", Content: []byte("more log data here")},
		},
	}
	<-sender.SendAsync(msg, &Status{})

	d := srv.Deliveries()
	if len(d) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(d))
	}
	body := string(d[0].Data)
	if !strings.Contains(body, "multipart/mixed") {
		t.Errorf("not multipart: %s", body)
	}
	if !strings.Contains(body, `filename="scenario.log"`) {
		t.Errorf("scenario.log attachment missing: %s", body)
	}
	if !strings.Contains(body, `filename="run.log"`) {
		t.Errorf("run.log attachment missing: %s", body)
	}
	// base64-encoded body content should be present
	if !strings.Contains(body, "TE9HIExJTkUg") { // "LOG LINE " in base64 prefix
		t.Errorf("attachment body bytes not encoded properly: %s", body)
	}
}

func TestSender_MultipleRecipients(t *testing.T) {
	srv := newMockServer(t, false)
	sender := srv.senderFor(Config{Enabled: true})

	msg := Message{
		From: "from@x",
		To:   []string{"a@x", "b@x"},
		CC:   []string{"c@x"},
		BCC:  []string{"d@x"},
		TextBody: "hi",
	}
	<-sender.SendAsync(msg, &Status{})

	d := srv.Deliveries()
	if len(d) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(d))
	}
	if len(d[0].To) != 4 {
		t.Errorf("expected 4 RCPT recipients (to+cc+bcc), got %d: %v", len(d[0].To), d[0].To)
	}
	body := string(d[0].Data)
	if !strings.Contains(body, "Cc: c@x") {
		t.Errorf("Cc header missing")
	}
	if strings.Contains(body, "Bcc:") {
		t.Errorf("BCC must not appear in headers")
	}
}

func TestSender_HTMLOnly(t *testing.T) {
	// HTML-only body → single-part text/html message, no text/plain part.
	srv := newMockServer(t, false)
	sender := srv.senderFor(Config{Enabled: true, From: "a@b", To: []string{"c@d"}})

	<-sender.SendAsync(Message{
		From: "a@b", To: []string{"c@d"}, Subject: "html",
		HTMLBody: "<p>hello</p>",
	}, &Status{})

	d := srv.Deliveries()
	if len(d) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(d))
	}
	body := string(d[0].Data)
	if !strings.Contains(body, "Content-Type: text/html") {
		t.Errorf("expected text/html Content-Type, got:\n%s", body)
	}
	if !strings.Contains(body, "<p>hello</p>") {
		t.Errorf("html body missing")
	}
	if strings.Contains(body, "multipart/") {
		t.Errorf("HTML-only message should not be multipart, got:\n%s", body)
	}
}

func TestSender_MultipartAlternative(t *testing.T) {
	// Both text and HTML bodies → multipart/alternative, both parts
	// present with the right Content-Types in the documented order
	// (text first, html last per RFC 2046 preference rule).
	srv := newMockServer(t, false)
	sender := srv.senderFor(Config{Enabled: true, From: "a@b", To: []string{"c@d"}})

	<-sender.SendAsync(Message{
		From: "a@b", To: []string{"c@d"}, Subject: "alt",
		TextBody: "PLAIN TEXT BODY",
		HTMLBody: "<p>HTML BODY</p>",
	}, &Status{})

	d := srv.Deliveries()
	if len(d) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(d))
	}
	body := string(d[0].Data)
	if !strings.Contains(body, "multipart/alternative") {
		t.Errorf("expected multipart/alternative, got:\n%s", body)
	}
	if !strings.Contains(body, "PLAIN TEXT BODY") {
		t.Errorf("plain text part missing")
	}
	if !strings.Contains(body, "<p>HTML BODY</p>") {
		t.Errorf("html part missing")
	}
	// Verify ordering: text/plain part comes before text/html so html
	// clients display the preferred-last alternative.
	textIdx := strings.Index(body, "Content-Type: text/plain")
	htmlIdx := strings.Index(body, "Content-Type: text/html")
	if textIdx < 0 || htmlIdx < 0 || textIdx >= htmlIdx {
		t.Errorf("text part should precede html part — got text=%d html=%d", textIdx, htmlIdx)
	}
}

func TestSender_MultipartAlternativeWithAttachments(t *testing.T) {
	// Body subtree (multipart/alternative) must be nested inside the
	// outer multipart/mixed, with attachments as siblings of the body
	// subtree — not children of either body part.
	srv := newMockServer(t, false)
	sender := srv.senderFor(Config{Enabled: true, From: "a@b", To: []string{"c@d"}})

	<-sender.SendAsync(Message{
		From: "a@b", To: []string{"c@d"}, Subject: "alt+att",
		TextBody: "text body",
		HTMLBody: "<p>html body</p>",
		Attachments: []Attachment{
			{Name: "log.txt", Content: []byte("log line")},
		},
	}, &Status{})

	d := srv.Deliveries()
	if len(d) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(d))
	}
	body := string(d[0].Data)
	if !strings.Contains(body, "multipart/mixed") {
		t.Errorf("outer envelope should be multipart/mixed")
	}
	if !strings.Contains(body, "multipart/alternative") {
		t.Errorf("inner body subtree should be multipart/alternative")
	}
	if !strings.Contains(body, `filename="log.txt"`) {
		t.Errorf("attachment missing")
	}
	if !strings.Contains(body, "text body") || !strings.Contains(body, "<p>html body</p>") {
		t.Errorf("body parts missing")
	}
}

func TestSender_DialFailure(t *testing.T) {
	// Point at an unreachable address so dialing fails fast and the
	// failure is recorded on the Status without any panic.
	cfg := Config{
		Enabled: true, From: "a@b", To: []string{"c@d"},
		SMTPHost: "127.0.0.1", SMTPPort: 1, // port 1 is reserved/not-listening
		SendTimeout: Duration{500 * time.Millisecond},
	}
	sender := NewSender(cfg)
	// Force the dialer to fail immediately rather than wait for OS-level
	// connect refusal to keep tests fast.
	sender.Dialer = func(network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("simulated dial failure: %s", addr)
	}
	status := &Status{}
	<-sender.SendAsync(Message{From: "a@b", To: []string{"c@d"}}, status)

	snap := status.Snapshot()
	if snap.State != StateFailed {
		t.Fatalf("expected Failed, got %s", snap.State)
	}
	if !strings.Contains(snap.Err.Error(), "simulated dial failure") {
		t.Errorf("error chain doesn't include dial cause: %v", snap.Err)
	}
}

func TestExtractAddress(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice@example.com", "alice@example.com"},
		{"Alice <alice@example.com>", "alice@example.com"},
		{"  Alice <alice@example.com>  ", "alice@example.com"},
		{"name@x.com>", "name@x.com>"}, // no opening bracket → returned as-is
	}
	for _, c := range cases {
		got := extractAddress(c.in)
		if got != c.want {
			t.Errorf("extractAddress(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"smtp.gmail.com", false},
		{"10.0.0.1", false},
	}
	for _, c := range cases {
		got := isLoopback(c.in)
		if got != c.want {
			t.Errorf("isLoopback(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestStatus_Transitions(t *testing.T) {
	s := &Status{}
	if s.State() != StateDisabled {
		t.Errorf("zero status should be Disabled, got %s", s.State())
	}
	s.MarkPending("a@b")
	if s.State() != StatePending {
		t.Errorf("after MarkPending: %s", s.State())
	}
	s.MarkSent()
	if s.State() != StateSent {
		t.Errorf("after MarkSent: %s", s.State())
	}
	s.MarkFailed(errors.New("boom"))
	snap := s.Snapshot()
	if snap.State != StateFailed || snap.Err == nil {
		t.Errorf("after MarkFailed: %+v", snap)
	}
}

func TestStatus_TriggerRoundTrips(t *testing.T) {
	// Trigger persists across state transitions so the TUI summary
	// always shows which event the latest email was about.
	s := &Status{}
	s.SetTrigger("progress")
	s.MarkPending("ops@x")
	s.MarkSent()
	snap := s.Snapshot()
	if snap.Trigger != "progress" {
		t.Errorf("Snapshot.Trigger: got %q want progress", snap.Trigger)
	}
	if !strings.Contains(s.Summary(), "(progress)") {
		t.Errorf("Summary should contain trigger in parentheses: %q", s.Summary())
	}

	// Setting a new trigger overwrites — what the TUI sees is always
	// the most recent send.
	s.SetTrigger("done")
	if got := s.Snapshot().Trigger; got != "done" {
		t.Errorf("expected trigger to update to 'done', got %q", got)
	}
}

// silence unused-import warning when the test runs in environments
// where net/smtp isn't otherwise referenced from this file.
var _ = smtp.PlainAuth
