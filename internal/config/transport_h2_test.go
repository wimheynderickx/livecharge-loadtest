package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTransport_Http2KeyAsBool(t *testing.T) {
	src := `
[transport]
type  = "http"
url   = "https://x"
http2 = false
`
	var c struct {
		Transport TransportConfig `toml:"transport"`
	}
	if _, err := toml.Decode(src, &c); err != nil {
		t.Fatalf("decode bool form: %v", err)
	}
	if c.Transport.HTTP2Opt == nil || *c.Transport.HTTP2Opt {
		t.Fatal("expected HTTP2Opt == &false")
	}
	if c.Transport.HTTP2 != nil {
		t.Fatal("HTTP2 sub-table should be nil for scalar form")
	}
}

func TestTransport_Http2KeyAsTable(t *testing.T) {
	src := `
[transport]
type = "http"
url  = "h2c://x"

[transport.http2]
max_concurrent_streams = 50
`
	var c struct {
		Transport TransportConfig `toml:"transport"`
	}
	if _, err := toml.Decode(src, &c); err != nil {
		t.Fatalf("decode table form: %v", err)
	}
	if c.Transport.HTTP2 == nil {
		t.Fatal("expected HTTP2 sub-table to decode")
	}
	if c.Transport.HTTP2.MaxConcurrentStreams != 50 {
		t.Errorf("MaxConcurrentStreams = %d", c.Transport.HTTP2.MaxConcurrentStreams)
	}
	if c.Transport.HTTP2Opt != nil {
		t.Fatal("HTTP2Opt should be nil for table form")
	}
}
