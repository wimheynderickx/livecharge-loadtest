package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"livecharge/loadtest/internal/manual"
	"livecharge/loadtest/internal/tui"
)

// newManualCmd registers the `manual` sub-command.
//
// Behaviour:
//   - stdout is a TTY:       render with glamour, page with a bubbletea
//                            viewport. q/esc to exit.
//   - stdout is not a TTY:   print raw markdown to stdout (so it can be
//                            redirected to a file or piped into another
//                            renderer like glow or bat).
//   - --raw flag:            always emit raw markdown, even on a TTY.
//   - --no-pager flag:       always render but skip pagination — write the
//                            rendered text straight to stdout.
func newManualCmd() *cobra.Command {
	var rawFlag, noPagerFlag bool

	cmd := &cobra.Command{
		Use:   "manual",
		Short: "Show the operational manual",
		Long: `Renders internal/manual/manual.md, the operational manual that
ships with this binary. With a TTY on stdout, the text is paged in a
scrollable viewport (q/esc to exit). Without a TTY, raw markdown is
printed to stdout — pipe to a renderer of your choice.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			isTTY := term.IsTerminal(int(os.Stdout.Fd()))

			// Without a TTY (or with --raw), emit raw markdown so the
			// output is meaningful when redirected to a file or piped
			// into glow/bat/less — glamour's ANSI escape codes would
			// be visual noise in those contexts.
			if rawFlag || !isTTY {
				fmt.Print(manual.Markdown())
				return nil
			}

			rendered := manual.Render(terminalWidth())

			// --no-pager: render with colour but skip the viewport so
			// the output stays in scrollback after the command exits.
			if noPagerFlag {
				fmt.Print(rendered)
				return nil
			}

			// Default TTY path: launch the viewport pager.
			p := tea.NewProgram(
				tui.NewManualPager(rendered),
				tea.WithAltScreen(),
			)
			_, err := p.Run()
			return err
		},
	}

	cmd.Flags().BoolVar(&rawFlag, "raw", false, "print raw markdown instead of rendered output")
	cmd.Flags().BoolVar(&noPagerFlag, "no-pager", false, "render but don't page; write to stdout")
	return cmd
}

// terminalWidth returns the column width of stdout when it's a TTY, or
// a sensible default (100) otherwise. Used for markdown word wrapping.
func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 100
	}
	return w
}
