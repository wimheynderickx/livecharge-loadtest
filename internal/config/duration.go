package config

import (
	"fmt"
	"time"
)

// Duration is a wrapper around time.Duration that knows how to parse itself
// from a TOML string value. We use a wrapper so that scenario files can write
// human-friendly durations like "2s" or "10m" instead of nanosecond integers.
//
// Example TOML:
//
//	response_timeout = "2s"
//
// The BurntSushi TOML library calls UnmarshalText on any type that implements
// encoding.TextUnmarshaler, so we satisfy that interface here.
type Duration struct {
	time.Duration
}

// UnmarshalText parses a Go duration string ("100ms", "5s", "1h30m", ...).
// It is called by the TOML decoder when it encounters a field of type Duration.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	d.Duration = parsed
	return nil
}

// MarshalText turns the Duration back into a string. We implement this for
// symmetry; in practice loadtest never writes TOML files, only reads them.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}
