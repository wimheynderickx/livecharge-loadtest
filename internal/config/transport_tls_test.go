package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

// minimalScenario returns a Validate-passing scenario you can mutate.
func minimalScenario(t *testing.T) *ScenarioConfig {
	t.Helper()
	return &ScenarioConfig{
		Scenario:  ScenarioMeta{Name: "t"},
		Transport: TransportConfig{Type: "http", URL: "http://localhost", Auth: AuthConfig{Type: "none"}},
		Load:      LoadConfig{TotalMessages: 1, Concurrency: 1, ResponseTimeout: Duration{1}},
		Metrics:   MetricsConfig{Percentiles: []float64{50}},
		Steps: []StepConfig{{
			Name: "s", Method: "GET", Path: "/", Template: "{}",
		}},
	}
}

func TestValidate_H2CWithHTTP2False_Rejected(t *testing.T) {
	cfg := minimalScenario(t)
	cfg.Transport.URL = "h2c://localhost:8080"
	f := false
	cfg.Transport.HTTP2Opt = &f
	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error: http2=false has no effect with h2c://")
	}
}

func TestValidate_CAFileMissing_Rejected(t *testing.T) {
	cfg := minimalScenario(t)
	cfg.Transport.URL = "https://api.example.com"
	cfg.Transport.TLS = &TLSConfig{CAFile: "/tmp/definitely-does-not-exist.pem"}
	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error: ca_file path does not exist")
	}
}

func TestValidate_TLSWithHTTPScheme_Rejected(t *testing.T) {
	cfg := minimalScenario(t)
	cfg.Transport.URL = "http://localhost:8080"
	cfg.Transport.TLS = &TLSConfig{InsecureSkipVerify: true}
	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error: [transport.tls] requires https:// scheme")
	}
}

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
	if cfg.Transport.HTTP2Opt == nil || *cfg.Transport.HTTP2Opt {
		t.Error("HTTP2Opt should decode as &false")
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
