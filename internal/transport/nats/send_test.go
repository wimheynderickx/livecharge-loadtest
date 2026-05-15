package nats_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	natsclient "github.com/nats-io/nats.go"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/transport"
	natstransport "livecharge/loadtest/internal/transport/nats"
)

// startServer launches an in-process NATS server on a random port.
// Tests get a ready-to-use NATS URL plus an auto-cleanup hook.
func startServer(t *testing.T) (string, *natsserver.Server) {
	t.Helper()
	opts := &natstest.DefaultTestOptions
	opts.Port = -1 // pick a free port
	srv := natstest.RunServer(opts)
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL(), srv
}

// startServerWithAuth launches a NATS server requiring user/password auth.
func startServerWithAuth(t *testing.T, user, pass string) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.Username = user
	opts.Password = pass
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

// subscribeReply runs an echo-style responder on subject that replies
// with the given body until the test ends.
func subscribeReply(t *testing.T, url, subject string, reply []byte) {
	t.Helper()
	c, err := natsclient.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if _, err := c.Subscribe(subject, func(m *natsclient.Msg) {
		if m.Reply != "" {
			out := &natsclient.Msg{Subject: m.Reply, Data: reply}
			if len(m.Header) > 0 {
				out.Header = natsclient.Header{}
				for k, vs := range m.Header {
					for _, v := range vs {
						out.Header.Add("X-Echo-"+k, v)
					}
				}
			}
			_ = c.PublishMsg(out)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestSend_RequestReply_HappyPath(t *testing.T) {
	url, _ := startServer(t)
	subscribeReply(t, url, "echo", []byte("pong"))

	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Subject: "echo", Body: []byte("ping"), Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "pong" {
		t.Errorf("Body = %q, want pong", string(resp.Body))
	}
	if resp.Latency <= 0 {
		t.Errorf("Latency = %v, want > 0", resp.Latency)
	}
}

func TestSend_HeadersForwardedBothWays(t *testing.T) {
	url, _ := startServer(t)
	subscribeReply(t, url, "echo", []byte("ok"))

	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Subject: "echo",
		Body:    []byte("ping"),
		Headers: map[string]string{"Trace-Id": "abc"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Meta["X-Echo-Trace-Id"] != "abc" {
		t.Errorf("Meta echo = %q, want abc (full meta = %v)", resp.Meta["X-Echo-Trace-Id"], resp.Meta)
	}
}

func TestSend_FireAndForget_NoSubscriberNoError(t *testing.T) {
	url, _ := startServer(t)

	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// No subscriber. FireAndForget must succeed anyway.
	resp, err := tr.Send(context.Background(), transport.Request{
		Subject:       "drop",
		Body:          []byte("ignored"),
		FireAndForget: true,
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("FireAndForget should not error: %v", err)
	}
	if resp.Body != nil || resp.Latency != 0 {
		t.Errorf("FireAndForget should return zero Response; got %+v", resp)
	}
}

func TestSend_FireAndForget_Delivered(t *testing.T) {
	url, _ := startServer(t)

	c, err := natsclient.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var got atomic.Int32
	sub, err := c.Subscribe("oneway", func(m *natsclient.Msg) {
		if string(m.Data) == "fire" {
			got.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	_ = c.Flush()

	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	_, err = tr.Send(context.Background(), transport.Request{
		Subject: "oneway", Body: []byte("fire"), FireAndForget: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for got.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got.Load() != 1 {
		t.Errorf("subscriber received %d messages, want 1", got.Load())
	}
}

func TestSend_Timeout_NoResponder(t *testing.T) {
	url, _ := startServer(t)

	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// No subscriber, request/reply path → timeout after 50ms.
	start := time.Now()
	_, err = tr.Send(context.Background(), transport.Request{
		Subject: "noone", Body: []byte("x"), Timeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want close to 50ms", elapsed)
	}
}

func TestSend_AfterClose_Errors(t *testing.T) {
	url, _ := startServer(t)
	subscribeReply(t, url, "echo", []byte("pong"))

	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = tr.Send(context.Background(), transport.Request{
		Subject: "echo", Body: []byte("x"), Timeout: 100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error after Close")
	}
}

func TestProtocol_AfterRealConnect(t *testing.T) {
	url, _ := startServer(t)
	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	got := tr.Protocol()
	if !strings.HasPrefix(got, "NATS ") || got == "NATS (connecting)" {
		t.Errorf("Protocol() = %q, want %q + version", got, "NATS")
	}
}

func TestNew_UnsupportedAuthType(t *testing.T) {
	url, _ := startServer(t)
	_, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "p"},
	})
	if err == nil {
		t.Fatal("expected error for basic auth on NATS")
	}
	if !strings.Contains(err.Error(), "unsupported auth type") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNew_ConnectFailure(t *testing.T) {
	_, err := natstransport.New(config.TransportConfig{
		Type: "nats",
		URL:  "nats://127.0.0.1:1", // port 1 is virtually never bound for NATS
		Auth: config.AuthConfig{Type: "none"},
	})
	if err == nil {
		t.Fatal("expected connect failure to a port with no listener")
	}
	if !strings.Contains(err.Error(), "nats connect") {
		t.Errorf("error = %q, want 'nats connect' prefix", err.Error())
	}
}

func TestNew_AuthUserPass_Success(t *testing.T) {
	url := startServerWithAuth(t, "alice", "secret")
	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url,
		Auth: config.AuthConfig{Type: "userpass", Username: "alice", Password: "secret"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tr.Close()
}

func TestNew_AuthUserPass_BadCreds(t *testing.T) {
	url := startServerWithAuth(t, "alice", "secret")
	_, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url,
		Auth: config.AuthConfig{Type: "userpass", Username: "alice", Password: "wrong"},
	})
	if err == nil {
		t.Fatal("expected auth failure with wrong password")
	}
}

func TestSend_ContextCancellation(t *testing.T) {
	url, _ := startServer(t)
	// No subscriber: request hangs until ctx is cancelled.
	tr, err := natstransport.New(config.TransportConfig{
		Type: "nats", URL: url, Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = tr.Send(ctx, transport.Request{
		Subject: "noone", Body: []byte("x"), Timeout: 5 * time.Second,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
	if elapsed > time.Second {
		t.Errorf("elapsed = %v, expected ~40ms after cancellation", elapsed)
	}
}

