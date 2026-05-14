package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"livecharge/loadtest/internal/config"
)

// newValidateCmd builds the "loadtest validate" sub-command.
//
// validate exits 0 on success and 1 (with all errors printed) when the file
// is malformed or fails cross-field validation.
func newValidateCmd() *cobra.Command {
	var (
		configPath string
		suitePath  string
		mockPath   string
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse a scenario, suite, or mock file and report any errors",
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case configPath != "":
				if _, err := config.LoadScenario(configPath); err != nil {
					return err
				}
				fmt.Println("OK")
				return nil
			case suitePath != "":
				if _, _, err := config.LoadSuite(suitePath); err != nil {
					return err
				}
				fmt.Println("OK")
				return nil
			case mockPath != "":
				if _, err := config.LoadMock(mockPath); err != nil {
					return err
				}
				fmt.Println("OK")
				return nil
			default:
				return fmt.Errorf("validate: provide one of --config, --suite, or --mock")
			}
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "path to a scenario TOML file")
	cmd.Flags().StringVar(&suitePath, "suite", "", "path to a suite TOML file")
	cmd.Flags().StringVar(&mockPath, "mock", "", "path to a mock-server TOML file")
	return cmd
}
