package config

import (
	"bytes"
	"fmt"

	"github.com/BurntSushi/toml"
)

// TransportConfig is the [transport] block in a scenario or mock TOML file.
// It tells loadtest which protocol to use (NATS or HTTP), the destination
// URL, and how to authenticate.
//
// The http2 key is decoded by UnmarshalTOML because it is dual-shape: it can
// be a scalar bool (http2 = false) stored in HTTP2Opt, or a config table
// ([transport.http2] with tuning knobs) stored in HTTP2.
type TransportConfig struct {
	// Type selects the transport implementation.
	// Valid values: "nats", "http", "https".
	Type string `toml:"type"`

	// URL is the destination address.
	//   - For NATS:  e.g. "nats://localhost:4222"
	//   - For HTTP:  e.g. "http://localhost:8080" (base URL; step paths are appended)
	//   - For HTTPS: e.g. "https://api.example.com"
	//   - For H2C:   e.g. "h2c://localhost:8080" (HTTP/2 over plain TCP, prior knowledge)
	URL string `toml:"url"`

	// Auth holds authentication settings. The Type field selects which
	// other fields are meaningful (basic auth uses username/password,
	// jwt uses token, etc.).
	Auth AuthConfig `toml:"auth"`

	// HTTP2Opt is set when the config uses the scalar form: http2 = false.
	// false means "disable h2 ALPN for https://"; nil means use the default
	// (h2 preferred). Has no effect on http:// or h2c://.
	// Set by UnmarshalTOML; do not add a toml struct tag.
	HTTP2Opt *bool

	// HTTP2 is set when the config uses the table form: [transport.http2].
	// It carries HTTP/2 server-tuning knobs. nil means "use defaults".
	// Set by UnmarshalTOML; do not add a toml struct tag.
	HTTP2 *HTTP2Config

	// TLS holds optional TLS knobs for HTTPS scenarios. nil means
	// "Go stdlib defaults" — verify against system roots, no SNI override.
	TLS *TLSConfig `toml:"tls"`
}

// HTTP2Config carries HTTP/2 tuning parameters for the [transport.http2]
// table. Most fields apply to the mock server side; MaxConcurrentStreams is
// also meaningful on the client (we surface it for symmetry, even though
// the Go http2.Transport does not currently honour it).
type HTTP2Config struct {
	// MaxConcurrentStreams caps the number of concurrent streams per
	// connection. 0 = library default.
	MaxConcurrentStreams int `toml:"max_concurrent_streams"`

	// InitialStreamWindowSize sets SETTINGS_INITIAL_WINDOW_SIZE.
	// Must fit in [1, 2^31-1]. 0 = library default.
	InitialStreamWindowSize int `toml:"initial_stream_window_size"`

	// InitialConnWindowSize sets the connection-level flow-control window.
	// Must be >= InitialStreamWindowSize. 0 = library default.
	InitialConnWindowSize int `toml:"initial_conn_window_size"`

	// MaxFrameSize sets SETTINGS_MAX_FRAME_SIZE.
	// Valid range: [16384, 16777215]. 0 = library default.
	MaxFrameSize int `toml:"max_frame_size"`
}

// UnmarshalTOML implements toml.Unmarshaler. It handles the dual-shape http2
// key: a bool scalar (http2 = false) is stored in HTTP2Opt, while a config
// table ([transport.http2]) is stored in HTTP2. All other fields are decoded
// through a re-encode round-trip so that struct tags on sub-types remain
// authoritative.
func (tc *TransportConfig) UnmarshalTOML(data interface{}) error {
	m, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Errorf("transport: expected TOML table, got %T", data)
	}

	if v, ok := m["type"].(string); ok {
		tc.Type = v
	}
	if v, ok := m["url"].(string); ok {
		tc.URL = v
	}
	if v, ok := m["auth"]; ok {
		if err := redecodeTOML(v, &tc.Auth); err != nil {
			return fmt.Errorf("transport.auth: %w", err)
		}
	}
	if v, ok := m["tls"]; ok {
		tc.TLS = new(TLSConfig)
		if err := redecodeTOML(v, tc.TLS); err != nil {
			return fmt.Errorf("transport.tls: %w", err)
		}
	}

	if v, ok := m["http2"]; ok {
		switch typed := v.(type) {
		case bool:
			tc.HTTP2Opt = &typed
		case map[string]interface{}:
			tc.HTTP2 = new(HTTP2Config)
			if err := redecodeTOML(typed, tc.HTTP2); err != nil {
				return fmt.Errorf("transport.http2: %w", err)
			}
		default:
			return fmt.Errorf("transport.http2: expected bool or table, got %T", v)
		}
	}

	return nil
}

// redecodeTOML re-encodes a raw TOML value (map[string]interface{} or scalar)
// back to TOML bytes, then decodes those bytes into dst using dst's struct
// tags. This keeps field-name strings authoritative in the struct definitions
// rather than duplicated inside custom UnmarshalTOML methods.
func redecodeTOML(raw interface{}, dst interface{}) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return err
	}
	_, err := toml.Decode(buf.String(), dst)
	return err
}

// TLSConfig carries TLS knobs shared by scenarios (client side) and mock
// servers (server side). Fields are optional unless noted.
type TLSConfig struct {
	// InsecureSkipVerify disables certificate verification. Required
	// when targeting servers with self-signed certs (e.g. the mock's
	// auto-generated cert). Triggers a one-time stderr warning at
	// scenario startup.
	InsecureSkipVerify bool `toml:"insecure_skip_verify"`

	// CAFile points to an additional PEM bundle to trust on top of the
	// system roots. Empty means "system roots only".
	CAFile string `toml:"ca_file"`

	// ServerName overrides the SNI value sent during the TLS handshake.
	// Defaults to the URL hostname.
	ServerName string `toml:"server_name"`

	// Enabled is mock-only: when true the mock binds a TLS listener.
	// Ignored on the client side (the client uses the URL scheme).
	Enabled bool `toml:"enabled"`

	// CertFile and KeyFile are mock-only: when both set the mock loads
	// the cert+key pair from disk. When both empty the mock generates a
	// self-signed cert in memory. Setting only one is a config error.
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

// AuthConfig describes how to authenticate with the server.
// Different transports support different auth types — see validate.go for
// the compatibility matrix.
type AuthConfig struct {
	// Type selects the auth mechanism.
	// Valid values:
	//   "none"     — no authentication (works for any transport)
	//   "userpass" — NATS-only: nats.UserInfo(username, password)
	//   "basic"    — HTTP/HTTPS-only: HTTP Basic Authentication header
	//   "jwt"      — HTTP/HTTPS-only: Authorization: Bearer <token>
	Type string `toml:"type"`

	// Username is used by "userpass" and "basic" auth.
	Username string `toml:"username"`

	// Password is used by "userpass" and "basic" auth.
	Password string `toml:"password"`

	// Token is used by "jwt" auth.
	Token string `toml:"token"`
}
