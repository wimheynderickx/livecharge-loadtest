package config

// ContextValueConfig describes one generator entry in the [context] block.
//
// The [context] section is unusual: it allows both bare scalar values
// (strings, integers, bools — these are static, copied as-is into every session)
// and table values that describe generators. We handle this by storing the raw
// TOML primitives during decode and resolving each entry in two passes — see
// internal/template/resolver.go for the resolution logic.
type ContextValueConfig struct {
	// Type selects the generator implementation.
	// Valid values: "sequence", "random_range", "random_pick".
	Type string `toml:"type"`

	// Sequence generator: Start is the first value produced, Step is the
	// amount added for each subsequent value. Both default to 1 if zero.
	Start int64 `toml:"start"`
	Step  int64 `toml:"step"`

	// random_range generator: Min and Max are inclusive bounds.
	Min int64 `toml:"min"`
	Max int64 `toml:"max"`

	// random_pick generator: Values is the list to pick from (one per session).
	Values []string `toml:"values"`
}
