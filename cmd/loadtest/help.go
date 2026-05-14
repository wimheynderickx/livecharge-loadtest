package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// newHelpCmd registers a custom `help` sub-command.
//
//	loadtest help            → brief curated overview (this command's body)
//	loadtest help <command>  → delegates to that sub-command's own --help
//
// We override Cobra's default help command (via SetHelpCommand on the root
// in main.go) so the no-argument form prints something hand-written and
// shorter than the auto-generated tree.
func newHelpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Brief overview, or help for a specific command",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				target, _, err := root.Find(args)
				if err != nil || target == nil {
					fmt.Fprintf(os.Stderr, "unknown command %q\n", strings.Join(args, " "))
					_ = root.Usage()
					return
				}
				_ = target.Help()
				return
			}
			fmt.Print(overviewText)
		},
	}
}

// overviewText is the brief intro shown by `loadtest help`.
//
// Keep it short: a one-paragraph description, the most-used commands with
// a one-line summary, and 2-3 example invocations. Users who want depth
// run `loadtest manual` or pass --help to a specific sub-command.
const overviewText = `Livecharge OCS LoadTest
=======================

A Go CLI for stress-testing services over NATS or HTTP. Multi-step
sessions, conditional branching via predicates, HDR latency histograms,
a live TUI dashboard, and a built-in mock server for self-contained tests.

Common commands
---------------
  run       Execute one scenario or a suite (TUI by default, --no-tui for headless)
  validate  Parse a scenario/suite/mock file and report any errors
  mock      Start the mock NATS/HTTP server defined by a mock TOML
  manual    Show the operational manual (paged when stdout is a TTY)
  help      This overview (or use "help <command>" for command-specific help)
  version   Print the build version

Examples
--------
  loadtest run --config scenarios/http-basic-auth.toml
  loadtest run --suite scenarios/suite-example.toml --no-tui
  loadtest mock --config mock/http-mock.toml
  loadtest validate --config scenarios/nats-session.toml
  loadtest manual

Inside the TUI press '1'-'4' to switch tabs, 's' to stop, 'r' to resume,
'R' to restart, 'm' for the manual, 'q' to quit.

Run "loadtest help <command>" or "loadtest <command> --help" for details.
`
