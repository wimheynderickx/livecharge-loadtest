package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestMockEndpoint_StreamBlock(t *testing.T) {
	src := `
[[endpoint]]
path = "/v1/stream"
ok_response = "abcabcabc"

  [endpoint.stream]
  chunks   = 3
  delay_ms = 25
`
	var c struct {
		Endpoints []MockEndpointConfig `toml:"endpoint"`
	}
	if _, err := toml.Decode(src, &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(c.Endpoints) != 1 {
		t.Fatalf("got %d endpoints", len(c.Endpoints))
	}
	st := c.Endpoints[0].Stream
	if st == nil {
		t.Fatal("Stream sub-table did not decode")
	}
	if st.Chunks != 3 {
		t.Errorf("Chunks = %d", st.Chunks)
	}
	if st.DelayMs != 25 {
		t.Errorf("DelayMs = %d", st.DelayMs)
	}
}
