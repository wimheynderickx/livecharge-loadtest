package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd builds the "loadtest version" sub-command. It prints the
// version and commit values injected via -ldflags at build time.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("loadtest %s (commit: %s)\n", version, commit)
			return nil
		},
	}
}
