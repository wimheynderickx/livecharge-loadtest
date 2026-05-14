package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// LoadScenario reads a scenario TOML file from path, decodes it, applies
// defaults, and runs cross-field validation. The returned LoadedScenario
// carries the parsed config plus the toml.MetaData needed for the second
// decode pass on the [context] section.
//
// On validation failure the returned error wraps a *ValidationErrors that
// lists every problem found, not just the first one.
func LoadScenario(path string) (*LoadedScenario, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve scenario path: %w", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read scenario file %s: %w", abs, err)
	}

	cfg := &ScenarioConfig{}
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("parse scenario TOML %s: %w", abs, err)
	}

	applyScenarioDefaults(cfg)

	if errs := ValidateScenario(cfg, md); len(errs) > 0 {
		return nil, &ValidationErrors{File: abs, Errors: errs}
	}

	return &LoadedScenario{
		Config:   cfg,
		MetaData: md,
		Path:     abs,
	}, nil
}

// LoadSuite reads a suite TOML file and resolves every referenced scenario
// path relative to the suite file's directory. Each referenced scenario is
// loaded and validated. Any override values in [[scenario.load]] are merged
// onto the underlying scenario's [load] block.
//
// The returned slice has the same length and order as cfg.Scenarios. The
// SuiteConfig itself is also returned for callers that need its metadata.
func LoadSuite(path string) (*SuiteConfig, []*LoadedScenario, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve suite path: %w", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("read suite file %s: %w", abs, err)
	}

	suite := &SuiteConfig{}
	if _, err := toml.Decode(string(data), suite); err != nil {
		return nil, nil, fmt.Errorf("parse suite TOML %s: %w", abs, err)
	}

	if len(suite.Scenarios) == 0 {
		return nil, nil, fmt.Errorf("suite %s contains no [[scenario]] entries", abs)
	}

	baseDir := filepath.Dir(abs)
	out := make([]*LoadedScenario, 0, len(suite.Scenarios))
	for i, ref := range suite.Scenarios {
		scenarioPath := ref.File
		if !filepath.IsAbs(scenarioPath) {
			scenarioPath = filepath.Join(baseDir, scenarioPath)
		}

		loaded, err := LoadScenario(scenarioPath)
		if err != nil {
			return nil, nil, fmt.Errorf("suite scenario #%d (%s): %w", i+1, ref.File, err)
		}

		if ref.Load != nil {
			mergeLoadOverride(&loaded.Config.Load, ref.Load)
		}

		out = append(out, loaded)
	}

	return suite, out, nil
}

// LoadMock reads a mock-server TOML file and runs validation.
func LoadMock(path string) (*MockConfig, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve mock path: %w", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read mock file %s: %w", abs, err)
	}

	cfg := &MockConfig{}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, fmt.Errorf("parse mock TOML %s: %w", abs, err)
	}

	if errs := ValidateMock(cfg); len(errs) > 0 {
		return nil, &ValidationErrors{File: abs, Errors: errs}
	}

	return cfg, nil
}

// applyScenarioDefaults fills in default values for fields the user omitted.
// Defaults are applied before validation so the validator sees the same
// values the runtime will use.
func applyScenarioDefaults(cfg *ScenarioConfig) {
	if cfg.Load.ResponseTimeout.Duration == 0 {
		cfg.Load.ResponseTimeout.Duration = defaultResponseTimeout
	}
	if cfg.Load.Concurrency == 0 {
		cfg.Load.Concurrency = 1
	}
	if len(cfg.Metrics.Percentiles) == 0 {
		cfg.Metrics.Percentiles = []float64{50, 75, 90, 95, 99, 99.9}
	}
	if cfg.Metrics.BucketCount == 0 && len(cfg.Metrics.BucketEdgesMs) == 0 {
		cfg.Metrics.BucketCount = 20
	}
	if cfg.Report != nil {
		if cfg.Report.TimestampFormat == "" {
			cfg.Report.TimestampFormat = "2006-01-02T15-04-05"
		}
		if cfg.Report.FlushInterval.Duration == 0 {
			cfg.Report.FlushInterval.Duration = defaultFlushInterval
		}
		if cfg.Report.Overwrite == nil {
			t := true
			cfg.Report.Overwrite = &t
		}
	}
}

// mergeLoadOverride copies non-zero fields from override into base.
// A zero value in the override means "use the scenario's own value".
func mergeLoadOverride(base, override *LoadConfig) {
	if override.Rate != 0 {
		base.Rate = override.Rate
	}
	if override.TotalMessages != 0 {
		base.TotalMessages = override.TotalMessages
	}
	if override.Duration.Duration != 0 {
		base.Duration = override.Duration
	}
	if override.Concurrency != 0 {
		base.Concurrency = override.Concurrency
	}
	if override.ResponseTimeout.Duration != 0 {
		base.ResponseTimeout = override.ResponseTimeout
	}
}
