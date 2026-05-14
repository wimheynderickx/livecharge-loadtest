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
	URL string `toml:"url"`

	// Auth holds authentication settings. The Type field selects which
	// other fields are meaningful (basic auth uses username/password,
	// jwt uses token, etc.).
	Auth AuthConfig `toml:"auth"`
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
