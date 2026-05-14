// Package main is the entry point for the loadtest CLI.
//
// The binary wires together the internal packages and exposes them through
// Cobra sub-commands:
//
//	loadtest run       — execute a scenario or suite (TUI or headless)
//	loadtest validate  — parse a scenario file and report any errors
//	loadtest mock      — start the mock NATS/HTTP server (added by Plan C)
//	loadtest version   — print build version
//
// main.go intentionally stays thin. All real logic lives in internal/*.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version and commit are injected at build time via -ldflags.
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc1234" ...
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	root := &cobra.Command{
		Use:   "loadtest",
		Short: "Livecharge OCS LoadTest — Go CLI load-testing tool",
		Long: `loadtest sends JSON payloads over NATS or HTTP, supports multi-step sessions
with conditional branching, measures latency and throughput, and renders a
live TUI dashboard. Configuration is entirely TOML-based.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newMockCmd())
	root.AddCommand(newManualCmd())
	// Override Cobra's auto-generated help command with our curated one.
	// Our help still delegates to `cmd.Help()` for `loadtest help <command>`,
	// so per-subcommand help continues to work.
	root.SetHelpCommand(newHelpCmd(root))

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
