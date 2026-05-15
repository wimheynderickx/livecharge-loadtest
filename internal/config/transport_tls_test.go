package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTransport_TLSBlock(t *testing.T) {
	src := `
[transport]
type = "http"
url  = "https://api.example.com"
http2 = false

[transport.tls]
insecure_skip_verify = true
ca_file              = "/etc/ssl/my-ca.pem"
server_name          = "real.example.com"
`
	var cfg struct {
		Transport TransportConfig `toml:"transport"`
	}
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Transport.HTTP2 == nil || *cfg.Transport.HTTP2 {
		t.Error("HTTP2 should decode as &false")
	}
	if cfg.Transport.TLS == nil {
		t.Fatal("TLS block should decode")
	}
	if !cfg.Transport.TLS.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
	if cfg.Transport.TLS.CAFile != "/etc/ssl/my-ca.pem" {
		t.Errorf("CAFile = %q", cfg.Transport.TLS.CAFile)
	}
	if cfg.Transport.TLS.ServerName != "real.example.com" {
		t.Errorf("ServerName = %q", cfg.Transport.TLS.ServerName)
	}
}
