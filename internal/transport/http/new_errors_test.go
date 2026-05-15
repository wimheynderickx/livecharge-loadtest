package http

import (
	"strings"
	"testing"

	"livecharge/loadtest/internal/config"
)

func TestNew_UnsupportedScheme(t *testing.T) {
	_, err := New(config.TransportConfig{
		Type: "http", URL: "ws://localhost:8080",
		Auth: config.AuthConfig{Type: "none"},
	})
	if err == nil {
		t.Fatal("expected error for ws:// scheme")
	}
	if !strings.Contains(err.Error(), "unsupported URL scheme") {
		t.Errorf("error = %q, want containing 'unsupported URL scheme'", err.Error())
	}
}

func TestNew_UnsupportedAuthType(t *testing.T) {
	_, err := New(config.TransportConfig{
		Type: "http", URL: "http://localhost:8080",
		Auth: config.AuthConfig{Type: "oauth2", Token: "x"},
	})
	if err == nil {
		t.Fatal("expected error for oauth2 auth type")
	}
	if !strings.Contains(err.Error(), "unsupported auth type") {
		t.Errorf("error = %q, want containing 'unsupported auth type'", err.Error())
	}
}

func TestNew_MalformedURL(t *testing.T) {
	// A control character in the URL makes url.Parse fail.
	_, err := New(config.TransportConfig{
		Type: "http", URL: "http://exa\x7fmple.com",
		Auth: config.AuthConfig{Type: "none"},
	})
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if !strings.Contains(err.Error(), "parse url") {
		t.Errorf("error = %q, want containing 'parse url'", err.Error())
	}
}

func TestNew_BadTLSConfigPropagates(t *testing.T) {
	_, err := New(config.TransportConfig{
		Type: "http", URL: "https://example.com",
		Auth: config.AuthConfig{Type: "none"},
		TLS:  &config.TLSConfig{CAFile: "/tmp/loadtest-no-such-ca.pem"},
	})
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Errorf("error = %q, want containing 'tls'", err.Error())
	}
}

func TestNew_AuthBasicPrebuiltsHeader(t *testing.T) {
	tr, err := New(config.TransportConfig{
		Type: "http", URL: "http://localhost:8080",
		Auth: config.AuthConfig{Type: "basic", Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tr.Close()
	// "u:p" base64-encoded is "dXA6cA==" (wait — "u:p" → "dTpw"). Confirm
	// the prefix and a stable suffix; full decoding is in std encoding tests.
	if !strings.HasPrefix(tr.preBuiltAuth, "Basic ") {
		t.Errorf("preBuiltAuth = %q, want Basic prefix", tr.preBuiltAuth)
	}
	if tr.preBuiltAuth != "Basic dTpw" {
		t.Errorf("preBuiltAuth = %q, want %q", tr.preBuiltAuth, "Basic dTpw")
	}
}

func TestNew_AuthJWTPrebuiltsHeader(t *testing.T) {
	tr, err := New(config.TransportConfig{
		Type: "http", URL: "http://localhost:8080",
		Auth: config.AuthConfig{Type: "jwt", Token: "abc.def.ghi"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer tr.Close()
	if tr.preBuiltAuth != "Bearer abc.def.ghi" {
		t.Errorf("preBuiltAuth = %q", tr.preBuiltAuth)
	}
}
