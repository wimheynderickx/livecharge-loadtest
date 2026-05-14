// Package config reads TOML scenario, suite, and mock-server files from disk
// and turns them into typed Go structs.
//
// Nothing else in the loadtest application reads TOML directly. Every other
// package depends only on the structs defined here. This keeps file-format
// concerns in one place and makes the rest of the codebase testable without
// touching the filesystem.
//
// The three top-level entry points are:
//
//   - LoadScenario(path)  reads a single scenario .toml file.
//   - LoadSuite(path)     reads a suite .toml file that references multiple scenarios.
//   - LoadMock(path)      reads a mock-server .toml file.
//
// Each entry point parses the TOML, then runs cross-field validation. If any
// validation rule fails, the function returns an error that lists every problem
// it found, not just the first one. This makes it easier to fix bad configs.
package config
