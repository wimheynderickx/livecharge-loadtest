package config

import (
	"strings"
	"testing"
)

func TestValidateMock_CertWithoutKey(t *testing.T) {
	mc := &MockConfig{
		Transport: TransportConfig{
			Type: "http",
			URL:  "localhost:8443",
			TLS:  &TLSConfig{Enabled: true, CertFile: "/tmp/cert.pem"}, // no KeyFile
		},
		Endpoints: []MockEndpointConfig{{Path: "/", OkResponse: "ok"}},
	}
	errs := ValidateMock(mc)
	if !containsErr(errs, "cert_file and key_file must both be set or both empty") {
		t.Fatalf("expected pair error; got %v", errs)
	}
}

func TestValidateMock_H2_WindowSizeRule(t *testing.T) {
	mc := &MockConfig{
		Transport: TransportConfig{
			Type: "http",
			URL:  "localhost:8080",
			HTTP2: &HTTP2Config{
				InitialStreamWindowSize: 1 << 20,
				InitialConnWindowSize:   1 << 10, // less than stream — invalid
			},
		},
		Endpoints: []MockEndpointConfig{{Path: "/", OkResponse: "ok"}},
	}
	errs := ValidateMock(mc)
	if !containsErr(errs, "initial_conn_window_size must be >= initial_stream_window_size") {
		t.Fatalf("expected window-size error; got %v", errs)
	}
}

func TestValidateMock_H2_MaxFrameSize(t *testing.T) {
	mc := &MockConfig{
		Transport: TransportConfig{
			Type: "http",
			URL:  "localhost:8080",
			HTTP2: &HTTP2Config{MaxFrameSize: 1024}, // below 16384
		},
		Endpoints: []MockEndpointConfig{{Path: "/", OkResponse: "ok"}},
	}
	errs := ValidateMock(mc)
	if !containsErr(errs, "max_frame_size must be between 16384 and 16777215") {
		t.Fatalf("expected frame size error; got %v", errs)
	}
}

func TestValidateMock_Stream_ChunksZero(t *testing.T) {
	mc := &MockConfig{
		Transport: TransportConfig{Type: "http", URL: "localhost:8080"},
		Endpoints: []MockEndpointConfig{{
			Path: "/", OkResponse: "ok",
			Stream: &StreamConfig{Chunks: 0, DelayMs: 0},
		}},
	}
	errs := ValidateMock(mc)
	if !containsErr(errs, "chunks must be >= 1") {
		t.Fatalf("expected chunks error; got %v", errs)
	}
}

func containsErr(errs []ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}
