package config

import (
	"github.com/BurntSushi/toml"

	"livecharge/loadtest/internal/mail"
)

// ScenarioConfig is the in-memory representation of a single scenario TOML file.
//
// The Context field is left as raw TOML primitives because the [context]
// section is heterogeneous: some entries are plain scalars (static values)
// and others are tables (generator descriptions). Two-pass decoding in
// internal/template/resolver.go turns each primitive into the appropriate
// generator.
type ScenarioConfig struct {
	// Scenario is the [scenario] block (name, description).
	Scenario ScenarioMeta `toml:"scenario"`

	// Transport is the [transport] block plus its [transport.auth] sub-block.
	Transport TransportConfig `toml:"transport"`

	// Context is the raw [context] section. Resolved later by the template package.
	// The toml.MetaData returned from the decoder is needed to inspect each
	// primitive, so we keep it alongside (see Loaded below).
	Context map[string]toml.Primitive `toml:"context"`

	// Load is the [load] block.
	Load LoadConfig `toml:"load"`

	// Metrics is the [metrics] block.
	Metrics MetricsConfig `toml:"metrics"`

	// Report is the optional [report] block. nil means "no CSV output".
	Report *ReportConfig `toml:"report"`

	// Email is the optional [email] block. nil means "no email
	// notifications for this scenario". When non-nil, the values become
	// the *floor* for the three-way merge — --mail-config and CLI flags
	// can override per field. See internal/mail/merge.go for precedence.
	Email *mail.Config `toml:"email"`

	// Steps is the ordered list of [[step]] blocks.
	Steps []StepConfig `toml:"step"`
}

// ScenarioMeta holds the [scenario] block fields.
type ScenarioMeta struct {
	// Name is the short identifier used everywhere (CSV rows, TUI sidebar, logs).
	Name string `toml:"name"`

	// Description is a free-form one-liner shown in the TUI and logs.
	Description string `toml:"description"`
}

// LoadedScenario bundles a parsed scenario with the metadata needed for
// secondary decoding (the [context] section in particular).
//
// LoadScenario returns one of these. Callers that need to resolve the
// context generators pass the MetaData to ContextFactory.New.
type LoadedScenario struct {
	Config   *ScenarioConfig
	MetaData toml.MetaData
	// Path is the absolute path the file was loaded from. Used to resolve
	// relative paths in CSV report destinations.
	Path string
	// Warnings are non-fatal config issues collected during load. The CLI
	// prints these to stderr at startup so users see them on every run.
	Warnings []ValidationWarning
}

// SuiteConfig is a suite TOML file that references multiple scenarios.
// Each [[scenario]] entry points to a separate scenario file and may
// override its [load] block.
type SuiteConfig struct {
	// Suite is the [suite] block.
	Suite SuiteMeta `toml:"suite"`

	// Scenarios is the list of [[scenario]] references.
	Scenarios []SuiteScenarioRef `toml:"scenario"`
}

// SuiteMeta holds the [suite] block fields.
type SuiteMeta struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
}

// SuiteScenarioRef is one [[scenario]] entry inside a suite file.
type SuiteScenarioRef struct {
	// File is the path to the scenario TOML, resolved relative to the
	// suite file's directory.
	File string `toml:"file"`

	// Description is a free-form description shown in logs. Optional.
	Description string `toml:"description"`

	// Load overrides the scenario's [load] block when non-nil.
	// Fields left zero in this override keep the scenario file's value.
	Load *LoadConfig `toml:"load"`
}
