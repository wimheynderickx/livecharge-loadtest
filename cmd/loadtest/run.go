package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
	"livecharge/loadtest/internal/manual"
	"livecharge/loadtest/internal/metrics"
	"livecharge/loadtest/internal/report"
	"livecharge/loadtest/internal/tui"
)

// newRunCmd builds the "loadtest run" sub-command.
//
// Plan A only implements the --no-tui path. The TUI flag is accepted but
// the absence of --no-tui is rejected until Plan B lands.
func newRunCmd() *cobra.Command {
	var (
		configPath string
		suitePath  string
		configDirs []string
		noTUI      bool
		logFile    string
		autoStart  bool
	)

	cmd := &cobra.Command{
		Use:   "run [flags] [file ...]",
		Short: "Run one or more scenarios or a suite",
	}

	mflags := addMailFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// explicit = positional args + --config + --suite (always start)
		// implicit = --config-dirs files not already in explicit
		//            (start only when --auto-start=true)
		explicit, implicit, err := resolveScenarios(args, configPath, suitePath, configDirs)
		if err != nil {
			return err
		}

		// Print non-fatal config warnings to stderr so users see them on every run.
		for _, ls := range append(explicit, implicit...) {
			for _, w := range ls.Warnings {
				fmt.Fprintf(os.Stderr, "WARN %s: %s — %s\n", ls.Path, w.Field, w.Message)
			}
		}

		// Pre-load the optional --mail-config. A bad path is NOT fatal:
		// we want the rest of the run to proceed and surface the error
		// in the TUI Overview tab so the user can clearly see why their
		// emails aren't going out. We warn on stderr for the headless
		// case where the TUI never opens.
		fileMail, fileMailErr := mail.LoadFile(mflags.configPath)
		if fileMailErr != nil {
			fmt.Fprintf(os.Stderr, "warning: --mail-config: %v\n", fileMailErr)
		}
		overrides := buildOverrides(cmd, mflags)
		registry := newMailRegistry()

		if noTUI {
			all := append(explicit, implicit...)
			return runHeadless(all, logFile, registry, fileMail, fileMailErr, overrides)
		}
		browseDirs := configDirs
		if len(browseDirs) == 0 {
			browseDirs = defaultBrowseDirs()
		}
		return runTUI(explicit, implicit, autoStart, logFile, browseDirs, registry, fileMail, fileMailErr, overrides)
	}

	cmd.Flags().StringVar(&configPath, "config", "", "path to a single scenario TOML file (always auto-starts)")
	cmd.Flags().StringVar(&suitePath, "suite", "", "path to a suite TOML file (always auto-starts)")
	cmd.Flags().StringSliceVar(&configDirs, "config-dirs", nil, "directories to scan for TOML scenario files")
	cmd.Flags().BoolVar(&noTUI, "no-tui", false, "headless mode — no TUI, print stats to stdout")
	cmd.Flags().StringVar(&logFile, "log-file", "", "append run log to this file")
	cmd.Flags().BoolVar(&autoStart, "auto-start", true, "automatically start --config-dirs scenarios; set false to leave them IDLE until Space is pressed in the TUI")

	return cmd
}

// defaultBrowseDirs returns the directories the TUI's 'a' picker and
// 'b' file-browser scan when --config-dirs is empty.
//
// The current working directory is always included. We then probe a
// short list of conventional subfolders and add the ones that exist as
// directories. This means users running from a typical project layout
// (./scenarios, ./templates, ./tests) get the picker populated without
// having to type --config-dirs every run; users with non-conventional
// layouts can still pass --config-dirs explicitly to override.
func defaultBrowseDirs() []string {
	dirs := []string{"."}
	for _, candidate := range []string{"./scenarios", "./scenario", "./templates", "./tests", "./test"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			dirs = append(dirs, candidate)
		}
	}
	return dirs
}

// resolveScenarios splits loaded scenarios into two groups:
//
//   - explicit: positional args + --config + --suite — always auto-start
//   - implicit: --config-dirs files not already in explicit — respect --auto-start flag
//
// Both lists are deduplicated. A file that appears in both explicit args and
// --config-dirs is placed only in explicit.
func resolveScenarios(args []string, configPath, suitePath string, dirs []string) ([]*config.LoadedScenario, []*config.LoadedScenario, error) {
	var explicit, implicit []*config.LoadedScenario

	// loadFile tries scenario first, then suite, and appends to dst.
	loadFile := func(path string, dst *[]*config.LoadedScenario) error {
		s, err := config.LoadScenario(path)
		if err != nil {
			_, list, suiteErr := config.LoadSuite(path)
			if suiteErr != nil {
				return err // original error is more informative
			}
			*dst = append(*dst, list...)
			return nil
		}
		*dst = append(*dst, s)
		return nil
	}

	// Positional args.
	for _, arg := range args {
		if err := loadFile(arg, &explicit); err != nil {
			return nil, nil, fmt.Errorf("load %s: %w", arg, err)
		}
	}
	// --config (single file).
	if configPath != "" {
		if err := loadFile(configPath, &explicit); err != nil {
			return nil, nil, err
		}
	}
	// --suite.
	if suitePath != "" {
		_, list, err := config.LoadSuite(suitePath)
		if err != nil {
			return nil, nil, err
		}
		explicit = append(explicit, list...)
	}

	// Build a set of paths already covered by explicit so the dir scan can
	// skip them and avoid duplicate entries in the TUI.
	explicitPaths := map[string]bool{}
	for _, s := range explicit {
		explicitPaths[s.Path] = true
	}

	// --config-dirs scan → implicit.
	seen := map[string]bool{}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			return nil, nil, fmt.Errorf("read dir %s: %w", d, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			p := filepath.Join(d, e.Name())
			abs, _ := filepath.Abs(p)
			if seen[abs] || explicitPaths[abs] {
				continue
			}
			seen[abs] = true

			s, scenarioErr := config.LoadScenario(p)
			if scenarioErr != nil {
				_, suiteScenarios, suiteErr := config.LoadSuite(p)
				if suiteErr != nil {
					// Neither a valid scenario nor a valid suite. This
					// is expected when --config-dirs points at a folder
					// that also holds a mail-config / mock-config / any
					// other TOML — we don't want one such file to crash
					// the whole run. Warn on stderr and skip; if the
					// user actually meant to run it, they can pass it
					// explicitly via positional arg or --config and get
					// the real parse error there.
					fmt.Fprintf(os.Stderr, "warning: skipping %s (not a valid scenario or suite: %v)\n", p, scenarioErr)
					continue
				}
				for _, ss := range suiteScenarios {
					if !seen[ss.Path] && !explicitPaths[ss.Path] {
						seen[ss.Path] = true
						implicit = append(implicit, ss)
					}
				}
				continue
			}
			implicit = append(implicit, s)
		}
	}

	if len(explicit) == 0 && len(implicit) == 0 {
		return nil, nil, errors.New("provide files, --config, --suite, or --config-dirs")
	}
	return explicit, implicit, nil
}

// runTUI starts all runners and hands control to the Bubble Tea program.
// Runners auto-start so the dashboard is alive on first render. The TUI
// itself owns lifecycle from there (stop/resume/restart/add/remove).
//
// logFile, when non-empty, mirrors every log line into a file in addition
// to the in-TUI Log tab.
// runTUI starts the Bubble Tea dashboard.
//
// explicit scenarios always auto-start. implicit scenarios (from --config-dirs)
// only auto-start when autoStart is true; otherwise they sit IDLE until the
// user presses Space in the TUI.
func runTUI(
	explicit, implicit []*config.LoadedScenario,
	autoStart bool,
	logFile string,
	browseDirs []string,
	registry *mailRegistry,
	fileMail mail.Config,
	fileMailErr error,
	overrides mail.Overrides,
) error {
	allLoaded := append(append([]*config.LoadedScenario{}, explicit...), implicit...)
	managed, err := buildManaged(allLoaded)
	if err != nil {
		return err
	}
	// Defer cleanup of whatever survives to quit. Each ManagedScenario
	// has its own OnRemove; we call them all here. Runners removed via
	// 'x' during the session were already torn down by the TUI.
	defer func() {
		for _, m := range managed {
			if m.OnRemove != nil {
				m.OnRemove()
			}
		}
	}()

	var logSink *os.File
	if logFile != "" {
		logSink, err = os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open log file %s: %w", logFile, err)
		}
		defer logSink.Close()
	}

	// Wire email lifecycle hooks for every scenario before any runner
	// starts so a fast-failing DONE can't race the callback installation.
	// wireScenarioMail folds config errors into the status (visible in
	// the TUI) rather than returning them — a single bad [email] block
	// must not take the whole dashboard down.
	//
	// We also wrap each scenario's OnRemove so cleanup (program exit OR
	// 'x' mid-session) also cancels any in-flight progress ticker.
	// Without this, a removed scenario's ticker would keep its goroutine
	// alive (idly skipping ticks while state != RUNNING) until process exit.
	for i, m := range managed {
		if _, err := wireScenarioMail(registry, allLoaded[i], m.Runner, fileMail, fileMailErr, overrides, logFile); err != nil {
			return err
		}
		runner := m.Runner
		prev := managed[i].OnRemove
		managed[i].OnRemove = func() {
			registry.stopProgress(runner)
			if prev != nil {
				prev()
			}
		}
	}

	// Start explicit runners unconditionally; implicit runners only when
	// autoStart is true. Implicit runners left IDLE can be started via Space.
	for i, m := range managed {
		if i < len(explicit) || autoStart {
			if err := m.Runner.Start(); err != nil {
				return fmt.Errorf("start %q: %w", m.Runner.Name(), err)
			}
		}
	}

	// Block on program exit for any in-flight email sends. Bounded by the
	// configured send_timeout so a slow server can't keep loadtest alive.
	defer registry.WaitAll(60 * time.Second)

	// Scan candidate scenarios from the browse dirs so 'a' has something
	// to offer. No pre-filtering here — the Model's activePaths handles
	// exclusion dynamically, so removed scenarios reappear automatically.
	candidates := scanCandidates(browseDirs)

	// Builder: invoked by the TUI when the user picks a candidate via
	// 'a' or selects a file via 'b'. Returns a ManagedScenario the TUI
	// then drives like any other entry in the sidebar.
	builder := func(path string) (tui.ManagedScenario, error) {
		return buildOneManaged(path)
	}

	// Pre-render the manual at TUI startup so the 'm' modal opens
	// instantly. We use a wide width here because the modal stretches
	// to the terminal's full width at runtime; word-wrap will look
	// slightly loose but never line-broken too aggressively.
	manualContent := manual.Render(120)

	var sink io.Writer
	if logSink != nil {
		sink = logSink
	}
	return tui.Run(tui.Config{
		Initial:      managed,
		LogSink:      sink,
		Manual:       manualContent,
		Candidates:   candidates,
		Builder:      builder,
		BrowserRoots: browseDirs,
		MailStatuses: registry,
	})
}

// buildManaged turns a slice of LoadedScenarios into ManagedScenarios,
// each carrying its own teardown callback that stops the runner, its
// CSV writer (if any), and closes the transport.
func buildManaged(scenarios []*config.LoadedScenario) ([]tui.ManagedScenario, error) {
	out := make([]tui.ManagedScenario, 0, len(scenarios))
	for _, s := range scenarios {
		ms, err := newManagedFromLoaded(s)
		if err != nil {
			// Roll back whatever we already built so connections don't
			// leak when one scenario in a suite fails.
			for _, prev := range out {
				if prev.OnRemove != nil {
					prev.OnRemove()
				}
			}
			return nil, err
		}
		out = append(out, ms)
	}
	return out, nil
}

// newManagedFromLoaded wraps a LoadedScenario in a ManagedScenario.
// Used both at startup (buildManaged) and at runtime (the 'a' / 'b'
// builder closure, via buildOneManaged).
func newManagedFromLoaded(s *config.LoadedScenario) (tui.ManagedScenario, error) {
	r, err := engine.NewRunner(s)
	if err != nil {
		return tui.ManagedScenario{}, fmt.Errorf("build runner for %q: %w", s.Config.Scenario.Name, err)
	}

	var writer *report.CSVWriter
	if s.Config.Report != nil {
		writer, err = report.NewCSVWriter(*s.Config.Report, r, s.Config.Metrics.Percentiles)
		if err != nil {
			_ = r.Close()
			return tui.ManagedScenario{}, fmt.Errorf("csv writer for %q: %w", s.Config.Scenario.Name, err)
		}
		writer.Start()
	}

	cleanup := func() {
		if r.State() == engine.StateRunning {
			_ = r.Stop()
		}
		if writer != nil {
			_ = writer.Stop()
		}
		_ = r.Close()
	}
	return tui.ManagedScenario{Runner: r, OnRemove: cleanup, Path: s.Path}, nil
}

// buildOneManaged loads, builds, and starts a fresh scenario from a
// TOML file path. Used by the TUI 'a' picker and 'b' file browser.
func buildOneManaged(path string) (tui.ManagedScenario, error) {
	loaded, err := config.LoadScenario(path)
	if err != nil {
		return tui.ManagedScenario{}, err
	}
	ms, err := newManagedFromLoaded(loaded)
	if err != nil {
		return tui.ManagedScenario{}, err
	}
	if err := ms.Runner.Start(); err != nil {
		ms.OnRemove()
		return tui.ManagedScenario{}, err
	}
	return ms, nil
}

// scanCandidates walks browseDirs for *.toml scenario files and returns
// them all as ScenarioCandidate values. No active-path filtering is done
// here — the Model's activePaths map handles that at picker-open time,
// which means removed scenarios automatically reappear in the picker.
func scanCandidates(dirs []string) []tui.ScenarioCandidate {
	var out []tui.ScenarioCandidate
	seen := map[string]bool{}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			full, err := filepath.Abs(filepath.Join(d, e.Name()))
			if err != nil || seen[full] {
				continue
			}
			seen[full] = true
			loaded, err := config.LoadScenario(full)
			if err != nil {
				// Not a scenario file (suite, mock, etc.); skip silently.
				continue
			}
			out = append(out, tui.ScenarioCandidate{
				Name:        loaded.Config.Scenario.Name,
				Description: loaded.Config.Scenario.Description,
				Path:        full,
			})
		}
	}
	return out
}

// stopWriters closes every CSVWriter, ignoring errors so a Stop failure on
// one writer doesn't mask a more important error.
func stopWriters(writers []*report.CSVWriter) {
	for _, w := range writers {
		_ = w.Stop()
	}
}

// buildRunners constructs Runners + CSVWriters for every scenario and
// returns them. On error, any partial state is cleaned up before returning.
func buildRunners(scenarios []*config.LoadedScenario) ([]*engine.Runner, []*report.CSVWriter, error) {
	runners := make([]*engine.Runner, 0, len(scenarios))
	for _, s := range scenarios {
		r, err := engine.NewRunner(s)
		if err != nil {
			closeAll(runners)
			return nil, nil, fmt.Errorf("build runner for %q: %w", s.Config.Scenario.Name, err)
		}
		runners = append(runners, r)
	}

	var writers []*report.CSVWriter
	for i, s := range scenarios {
		if s.Config.Report == nil {
			continue
		}
		w, err := report.NewCSVWriter(*s.Config.Report, runners[i], s.Config.Metrics.Percentiles)
		if err != nil {
			closeAll(runners)
			stopWriters(writers)
			return nil, nil, fmt.Errorf("csv writer for %q: %w", s.Config.Scenario.Name, err)
		}
		w.Start()
		writers = append(writers, w)
	}
	return runners, writers, nil
}

// runHeadless drives the engine without a TUI: start runners, print a
// progress line every five seconds, wait for completion or signals, print
// final summary.
func runHeadless(scenarios []*config.LoadedScenario, logFile string, registry *mailRegistry, fileMail mail.Config, fileMailErr error, overrides mail.Overrides) error {
	runners := make([]*engine.Runner, 0, len(scenarios))
	for _, s := range scenarios {
		r, err := engine.NewRunner(s)
		if err != nil {
			closeAll(runners)
			return fmt.Errorf("build runner for %q: %w", s.Config.Scenario.Name, err)
		}
		runners = append(runners, r)
	}
	defer closeAll(runners)

	// CSV writers: one per scenario that has [report] configured.
	var writers []*report.CSVWriter
	for i, s := range scenarios {
		if s.Config.Report == nil {
			continue
		}
		w, err := report.NewCSVWriter(*s.Config.Report, runners[i], s.Config.Metrics.Percentiles)
		if err != nil {
			return fmt.Errorf("csv writer for %q: %w", s.Config.Scenario.Name, err)
		}
		w.Start()
		writers = append(writers, w)
	}
	defer func() {
		for _, w := range writers {
			_ = w.Stop()
		}
	}()

	// Wire email lifecycle hooks before any runner starts. Config errors
	// surface as a Failed mail status (printed by the headless summary).
	for i, r := range runners {
		if _, err := wireScenarioMail(registry, scenarios[i], r, fileMail, fileMailErr, overrides, logFile); err != nil {
			return err
		}
	}
	// Stop any in-flight progress tickers on exit so they don't outlive
	// the runners (and so WaitAll below doesn't deadlock on a ticker that
	// has a pending send queued).
	defer func() {
		for _, r := range runners {
			registry.stopProgress(r)
		}
	}()
	// Wait for any in-flight email sends on exit.
	defer registry.WaitAll(60 * time.Second)

	// Signal handling so Ctrl-C drains cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, r := range runners {
		// Skip runners that hit a script error (bad expr predicate) — they
		// cannot start, but we still include them in the final summary.
		if r.State() == engine.StateScriptError {
			continue
		}
		if err := r.Start(); err != nil {
			return fmt.Errorf("start %q: %w", r.Name(), err)
		}
	}

	// Print progress every 5 seconds; stop when all runners finish or ctx cancels.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	done := waitAll(runners)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "interrupt received; stopping scenarios…")
			for _, r := range runners {
				_ = r.Stop()
			}
			printSummary(runners, logFile)
			return nil
		case <-done:
			printSummary(runners, logFile)
			return nil
		case <-ticker.C:
			printProgress(runners)
		}
	}
}

// waitAll returns a channel that closes when every runner has finished
// (state DONE or ERROR). It avoids blocking the main loop on a sync.WaitGroup.
func waitAll(runners []*engine.Runner) <-chan struct{} {
	ch := make(chan struct{})
	var wg sync.WaitGroup
	for _, r := range runners {
		// Skip runners that are in a terminal state before starting
		// (e.g., StateScriptError means they never entered the run loop).
		if state := r.State(); state == engine.StateDone || state == engine.StateError || state == engine.StateScriptError {
			continue
		}
		wg.Add(1)
		go func(r *engine.Runner) {
			defer wg.Done()
			<-r.DoneCh()
		}(r)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}

// closeAll releases transports. Used in error paths and defers.
func closeAll(runners []*engine.Runner) {
	for _, r := range runners {
		_ = r.Close()
	}
}

// printProgress writes a one-line update per runner to stdout.
func printProgress(runners []*engine.Runner) {
	for _, r := range runners {
		s := r.Snapshot()
		fmt.Printf("[%s] %-20s sent=%-7d recv=%-7d err=%-5d rate=%6.1f/s p99=%s\n",
			s.StateName,
			truncate(s.ScenarioName, 20),
			s.Sent, s.Received, s.Errors, s.MsgPerSec,
			metrics.FormatLatency(s.Percentiles[99]),
		)
	}
}

// printSummary prints a final table to stdout and (optionally) appends it
// to logFile.
func printSummary(runners []*engine.Runner, logFile string) {
	var b strings.Builder
	b.WriteString("\n=== Run summary ===\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SCENARIO\tSTATE\tSENT\tRECEIVED\tERRORS\tP50\tP95\tP99")
	for _, r := range runners {
		if r.State() == engine.StateScriptError {
			msg := r.ScriptError()
			if msg == "" {
				msg = "(no detail)"
			}
			fmt.Fprintf(tw, "%s\tSCRIPT_ERROR\t — %s\n", r.Name(), msg)
			continue
		}
		s := r.Snapshot()
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\n",
			s.ScenarioName,
			s.StateName,
			s.Sent, s.Received, s.Errors,
			metrics.FormatLatency(s.Percentiles[50]),
			metrics.FormatLatency(s.Percentiles[95]),
			metrics.FormatLatency(s.Percentiles[99]),
		)
	}
	tw.Flush()

	fmt.Print(b.String())

	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = f.WriteString(b.String())
			_ = f.Close()
		}
	}
}

// truncate cuts s to at most max runes, padding with spaces if shorter.
// Keeps printProgress columns aligned even with long scenario names.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// Compile-time check that *engine.Runner satisfies the report SnapshotSource
// contract. If this fails to compile we know report and engine have drifted.
var _ report.SnapshotSource = (*engine.Runner)(nil)

// metrics import is used transitively (engine.Snapshot returns metrics.Snapshot).
var _ = metrics.Snapshot{}
