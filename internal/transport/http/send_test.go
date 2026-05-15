package http_test

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/transport"
	httptransport "livecharge/loadtest/internal/transport/http"
)

// echoEnvelope is what reflectingServer returns: a JSON snapshot of the
// method, path, headers, and body the handler observed.
type echoEnvelope struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// reflectingServer returns a server that echoes the request shape back as
// JSON. The status field on its zero value (200) is overridden when the
// query string includes ?status=NNN, and a delay is honoured when
// ?delay_ms=NNN is present — used by timeout tests.
func reflectingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if d := r.URL.Query().Get("delay_ms"); d != "" {
			ms, _ := time.ParseDuration(d + "ms")
			time.Sleep(ms)
		}
		body, _ := io.ReadAll(r.Body)
		env := echoEnvelope{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: map[string]string{},
			Body:    string(body),
		}
		for k, vs := range r.Header {
			if len(vs) > 0 {
				env.Headers[k] = vs[0]
			}
		}
		// Optional multi-value header for the first-value-wins test.
		w.Header().Add("X-Multi", "first")
		w.Header().Add("X-Multi", "second")
		if s := r.URL.Query().Get("status"); s != "" {
			switch s {
			case "418":
				w.WriteHeader(nethttp.StatusTeapot)
			case "500":
				w.WriteHeader(nethttp.StatusInternalServerError)
			}
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSend_BasicAuthHeaderInjected(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Method: "GET", Path: "/", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := decodeEnvelope(t, resp.Body)
	if env.Headers["Authorization"] != "Basic dTpw" {
		t.Errorf("Authorization = %q, want Basic dTpw", env.Headers["Authorization"])
	}
}

func TestSend_JWTAuthHeaderInjected(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "jwt", Token: "TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Method: "GET", Path: "/", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := decodeEnvelope(t, resp.Body)
	if env.Headers["Authorization"] != "Bearer TOKEN" {
		t.Errorf("Authorization = %q", env.Headers["Authorization"])
	}
}

func TestSend_CallerSuppliedAuthWins(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Method:  "GET",
		Path:    "/",
		Headers: map[string]string{"Authorization": "Bearer caller-wins"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := decodeEnvelope(t, resp.Body)
	if env.Headers["Authorization"] != "Bearer caller-wins" {
		t.Errorf("Authorization = %q, caller value should win", env.Headers["Authorization"])
	}
}

func TestSend_DefaultContentTypeOnlyWhenBodyAndUnset(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// With body, no Content-Type → server sees application/json.
	resp, err := tr.Send(context.Background(), transport.Request{
		Method: "POST", Path: "/", Body: []byte(`{"x":1}`), Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := decodeEnvelope(t, resp.Body)
	if env.Headers["Content-Type"] != "application/json" {
		t.Errorf("default Content-Type missing; got %q", env.Headers["Content-Type"])
	}

	// Body + caller-supplied Content-Type → caller wins.
	resp, err = tr.Send(context.Background(), transport.Request{
		Method:  "POST",
		Path:    "/",
		Body:    []byte(`<x/>`),
		Headers: map[string]string{"Content-Type": "application/xml"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	env = decodeEnvelope(t, resp.Body)
	if env.Headers["Content-Type"] != "application/xml" {
		t.Errorf("Content-Type = %q, caller value should win", env.Headers["Content-Type"])
	}

	// No body → no default Content-Type injected.
	resp, err = tr.Send(context.Background(), transport.Request{
		Method: "GET", Path: "/", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	env = decodeEnvelope(t, resp.Body)
	if env.Headers["Content-Type"] != "" {
		t.Errorf("empty body should not trigger Content-Type; got %q", env.Headers["Content-Type"])
	}
}

func TestSend_TimeoutEnforced(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// Server delays 200ms, client times out at 50ms → error.
	_, err = tr.Send(context.Background(), transport.Request{
		Method:  "GET",
		Path:    "/?delay_ms=200",
		Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSend_NonOKStatusReturnedWithoutError(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	for _, code := range []int{418, 500} {
		resp, err := tr.Send(context.Background(), transport.Request{
			Method: "GET", Path: "/?status=" + itoa(code), Timeout: time.Second,
		})
		if err != nil {
			t.Errorf("status %d: Send returned error %v; should not", code, err)
		}
		if resp.StatusCode != code {
			t.Errorf("StatusCode = %d, want %d", resp.StatusCode, code)
		}
	}
}

func TestSend_MultiValueHeaderFirstValueWins(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Method: "GET", Path: "/", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Headers["X-Multi"] != "first" {
		t.Errorf("X-Multi = %q, want 'first' (first-value-wins)", resp.Headers["X-Multi"])
	}
}

func TestSend_LatencyMeasured(t *testing.T) {
	srv := reflectingServer(t)
	tr, err := httptransport.New(config.TransportConfig{
		Type: "http", URL: srv.URL,
		Auth: config.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	resp, err := tr.Send(context.Background(), transport.Request{
		Method: "GET", Path: "/?delay_ms=20", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Latency < 15*time.Millisecond {
		t.Errorf("Latency = %v, want >= 15ms (server delayed 20ms)", resp.Latency)
	}
	if resp.Latency > 500*time.Millisecond {
		t.Errorf("Latency = %v, suspiciously high", resp.Latency)
	}
}

func decodeEnvelope(t *testing.T, body []byte) echoEnvelope {
	t.Helper()
	var env echoEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	return env
}

// itoa is a tiny local helper so we don't pull in strconv just for two
// status codes.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
