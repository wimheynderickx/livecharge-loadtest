// Package nats implements transport.Transport on top of NATS.
//
// One Transport instance owns one nats.Conn. The client library is safe for
// concurrent use, so all sessions of a scenario can share the same Transport.
package nats

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	natsclient "github.com/nats-io/nats.go"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/transport"
)

// Transport sends requests over NATS.
type Transport struct {
	conn  *natsclient.Conn
	proto atomic.Value // string — set by captureVersion after Connect
}

// New connects to the NATS server described in cfg and returns a Transport
// ready to send messages.
func New(cfg config.TransportConfig) (*Transport, error) {
	opts := []natsclient.Option{
		natsclient.Name("loadtest"),
		natsclient.MaxReconnects(-1), // reconnect forever; load tools shouldn't die on a flaky link
		natsclient.ReconnectWait(time.Second),
	}

	switch cfg.Auth.Type {
	case "", "none":
		// nothing to add
	case "userpass":
		opts = append(opts, natsclient.UserInfo(cfg.Auth.Username, cfg.Auth.Password))
	default:
		return nil, fmt.Errorf("nats transport: unsupported auth type %q", cfg.Auth.Type)
	}

	conn, err := natsclient.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", cfg.URL, err)
	}

	t := &Transport{conn: conn}
	t.captureVersion()
	return t, nil
}

// captureVersion records the connected server's version into the proto
// atomic so Protocol() returns the right value after Connect.
func (t *Transport) captureVersion() {
	if t.conn == nil {
		t.proto.Store("NATS (connecting)")
		return
	}
	ver := t.conn.ConnectedServerVersion()
	if ver == "" {
		t.proto.Store("NATS")
		return
	}
	t.proto.Store("NATS " + ver)
}

// Send publishes the request and, unless FireAndForget is set, waits for a
// reply on the built-in inbox subject. Latency is the time between calling
// RequestMsgWithContext and the reply arriving.
func (t *Transport) Send(ctx context.Context, req transport.Request) (transport.Response, error) {
	msg := &natsclient.Msg{
		Subject: req.Subject,
		Data:    req.Body,
	}
	if len(req.Headers) > 0 {
		msg.Header = natsclient.Header{}
		for k, v := range req.Headers {
			msg.Header.Set(k, v)
		}
	}

	// Fire-and-forget: publish, return immediately. No latency, no reply.
	if req.FireAndForget {
		if err := t.conn.PublishMsg(msg); err != nil {
			return transport.Response{}, fmt.Errorf("nats publish: %w", err)
		}
		return transport.Response{}, nil
	}

	// Request/reply path. Apply the per-request timeout via context.
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	start := time.Now()
	reply, err := t.conn.RequestMsgWithContext(ctx, msg)
	latency := time.Since(start)
	if err != nil {
		return transport.Response{Latency: latency}, fmt.Errorf("nats request: %w", err)
	}

	resp := transport.Response{
		Body:    reply.Data,
		Latency: latency,
	}
	if len(reply.Header) > 0 {
		resp.Meta = make(map[string]string, len(reply.Header))
		for k, vs := range reply.Header {
			if len(vs) > 0 {
				resp.Meta[k] = vs[0]
			}
		}
	}
	return resp, nil
}

// Close drains the NATS connection. After Close, Send must not be called.
func (t *Transport) Close() error {
	if t.conn != nil {
		t.conn.Close()
	}
	return nil
}

// Protocol returns the NATS server version captured at connect time,
// e.g. "NATS 2.10". Returns "NATS (connecting)" when no connection exists.
func (t *Transport) Protocol() string {
	if v, ok := t.proto.Load().(string); ok && v != "" {
		return v
	}
	return "NATS (connecting)"
}
