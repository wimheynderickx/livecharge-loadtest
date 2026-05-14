package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/mockserver"
)

// newMockCmd starts the standalone mock NATS subscriber / HTTP server.
//
// The mock reads endpoint definitions from a TOML file, listens for
// requests, optionally extracts JSON fields, and replies with a templated
// OK or FAIL response. It runs until interrupted (Ctrl-C or SIGTERM).
func newMockCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "mock",
		Short: "Start the mock NATS/HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath == "" {
				return fmt.Errorf("mock: --config is required")
			}
			cfg, err := config.LoadMock(configPath)
			if err != nil {
				return err
			}
			srv, err := mockserver.NewMockServer(*cfg)
			if err != nil {
				return err
			}
			if err := srv.Start(); err != nil {
				return err
			}

			fmt.Printf("Mock server listening on %s (%s)\n  endpoints: %s\n  Press Ctrl-C to stop.\n",
				cfg.Transport.URL,
				cfg.Transport.Type,
				strings.Join(srv.Endpoints(), ", "),
			)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-ctx.Done()

			fmt.Fprintln(os.Stderr, "\nshutting down…")
			return srv.Stop()
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "path to a mock-server TOML file (required)")
	return cmd
}
