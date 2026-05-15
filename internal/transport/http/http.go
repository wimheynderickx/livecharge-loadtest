// Package http implements transport.Transport on top of net/http.
//
// One Transport instance owns one *http.Client. The Go HTTP client is safe
// for concurrent use and pools connections internally, so all sessions of a
// scenario share the same Transport — re-creating the client per session
// would defeat connection reuse and cripple throughput.
package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/transport"
)

// Transport sends requests over HTTP or HTTPS.
type Transport struct {
	client  *nethttp.Client
	baseURL string
	auth    config.AuthConfig

	// preBuiltAuth caches the value of the Authorization header so we don't
	// rebuild the same Basic/Bearer string on every request.
	preBuiltAuth string
}

// New constructs a Transport with a shared http.Client tuned for sustained
// load: many idle conns per host, short keep-alive timeout to avoid stale
// sockets, and a sensible read/write timeout.
func New(cfg config.TransportConfig) (*Transport, error) {
	tr := &nethttp.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     30 * time.Second,
	}
	t := &Transport{
		client: &nethttp.Client{
			Transport: tr,
			// We rely on per-request context timeouts; the client-level
			// timeout would cap every call regardless of step overrides.
			Timeout: 0,
		},
		baseURL: strings.TrimRight(cfg.URL, "/"),
		auth:    cfg.Auth,
	}

	switch cfg.Auth.Type {
	case "", "none":
		// no header
	case "basic":
		creds := cfg.Auth.Username + ":" + cfg.Auth.Password
		t.preBuiltAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
	case "jwt":
		t.preBuiltAuth = "Bearer " + cfg.Auth.Token
	default:
		return nil, fmt.Errorf("http transport: unsupported auth type %q", cfg.Auth.Type)
	}

	return t, nil
}

// Send issues one HTTP request and returns the parsed response.
//
// Latency is measured around client.Do; we don't include body-reading time
// in the per-step latency because a slow client (i.e. us reading the body)
// would inflate the server-side measurement we actually care about.
func (t *Transport) Send(ctx context.Context, req transport.Request) (transport.Response, error) {
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	url := t.baseURL + req.Path
	httpReq, err := nethttp.NewRequestWithContext(ctx, req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		return transport.Response{}, fmt.Errorf("http build request: %w", err)
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	// Caller-supplied Authorization wins over auto-generated.
	if t.preBuiltAuth != "" && httpReq.Header.Get("Authorization") == "" {
		httpReq.Header.Set("Authorization", t.preBuiltAuth)
	}
	if httpReq.Header.Get("Content-Type") == "" && len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	httpResp, err := t.client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		return transport.Response{Latency: latency}, fmt.Errorf("http send: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return transport.Response{Latency: latency, StatusCode: httpResp.StatusCode},
			fmt.Errorf("http read body: %w", err)
	}

	headers := make(map[string]string, len(httpResp.Header))
	for k, vs := range httpResp.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	return transport.Response{
		Body:       body,
		Headers:    headers,
		StatusCode: httpResp.StatusCode,
		Latency:    latency,
	}, nil
}

// Close shuts down the underlying connection pool. The Go HTTP client has
// no explicit close, so we just signal idle connections to close.
func (t *Transport) Close() error {
	if tr, ok := t.client.Transport.(*nethttp.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}

// Protocol returns a placeholder string until Task 4 wires the real
// tracker. Keeps the package compiling while the interface is in flux.
func (t *Transport) Protocol() string {
	return "HTTP/1.1 (intent)"
}
