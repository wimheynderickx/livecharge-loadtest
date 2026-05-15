package config

// TransportConfig is the [transport] block in a scenario or mock TOML file.
// It tells loadtest which protocol to use (NATS or HTTP), the destination
// URL, and how to authenticate.
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

	// HTTP2, when explicitly set to false, disables HTTP/2 over ALPN for
	// https:// URLs (forces h1.1). Unset (nil) keeps the default
	// behaviour (h2 preferred). Has no effect on http:// or h2c://.
	HTTP2 *bool `toml:"http2"`

	// TLS holds optional TLS knobs for HTTPS scenarios. nil means
	// "Go stdlib defaults" — verify against system roots, no SNI override.
	TLS *TLSConfig `toml:"tls"`
}

// TLSConfig carries the TLS knobs a load-test scenario may need.
// All fields are optional.
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
