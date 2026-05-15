package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/engine"
	"livecharge/loadtest/internal/mail"
	"livecharge/loadtest/internal/metrics"
)

// mailFlags holds the raw values of every email-related CLI flag plus the
// flagset itself so we can inspect Changed() per field. Each field stays
// at its zero value until cobra populates it; only flags the user actually
// typed get pushed into mail.Overrides.
type mailFlags struct {
	configPath string

	enabled   bool
	disabled  bool
	to        []string
	cc        []string
	bcc       []string
	from      string
	subject   string
	smtpHost  string
	smtpPort  int
	smtpUser  string
	smtpPass  string
	template  string
	attachLog bool
	on        []string
}

// addMailFlags attaches every --mail-* flag to cmd and returns a pointer
// to the populated mailFlags struct. The returned struct is wired to the
// underlying pflag values so cmd.Flags().Changed(name) works for each.
func addMailFlags(cmd *cobra.Command) *mailFlags {
	f := &mailFlags{}
	cmd.Flags().StringVar(&f.configPath, "mail-config", "", "load shared email settings from this TOML file (overrides scenario [email] block)")
	cmd.Flags().BoolVar(&f.enabled, "mail-enabled", false, "force-enable post-run email notifications (overrides scenario/file)")
	cmd.Flags().BoolVar(&f.disabled, "no-mail", false, "force-disable post-run email notifications regardless of scenario/file")
	cmd.Flags().StringSliceVar(&f.to, "mail-to", nil, "comma-separated To: recipients")
	cmd.Flags().StringSliceVar(&f.cc, "mail-cc", nil, "comma-separated Cc: recipients")
	cmd.Flags().StringSliceVar(&f.bcc, "mail-bcc", nil, "comma-separated Bcc: recipients")
	cmd.Flags().StringVar(&f.from, "mail-from", "", "From: address (default \"Livecharge OCS LoadTest <noreply@livecharge.local>\")")
	cmd.Flags().StringVar(&f.subject, "mail-subject", "", "subject template (text/template syntax, see manual)")
	cmd.Flags().StringVar(&f.smtpHost, "mail-smtp-host", "", "SMTP server hostname")
	cmd.Flags().IntVar(&f.smtpPort, "mail-smtp-port", 0, "SMTP server port (default 587)")
	cmd.Flags().StringVar(&f.smtpUser, "mail-smtp-user", "", "SMTP auth username")
	cmd.Flags().StringVar(&f.smtpPass, "mail-smtp-pass", "", "SMTP auth password (or set $LOADTEST_SMTP_PASS)")
	cmd.Flags().StringVar(&f.template, "mail-template", "", "path to body template file (defaults to built-in)")
	cmd.Flags().BoolVar(&f.attachLog, "mail-attach-log", false, "attach scenario log + --log-file contents to the email")
	cmd.Flags().StringSliceVar(&f.on, "mail-on", nil, "lifecycle events that trigger the email (done,error)")
	return f
}

// buildOverrides converts the cobra flag state into a mail.Overrides
// where each field is set only when the user actually typed the flag.
// This lets Merge tell "not set" from "set to zero" (e.g. --no-mail).
func buildOverrides(cmd *cobra.Command, f *mailFlags) mail.Overrides {
	o := mail.Overrides{}
	changed := func(name string) bool { return cmd.Flags().Changed(name) }

	// --mail-enabled and --no-mail are two flags driving the same field.
	// --no-mail wins when both are passed (it's the safer default).
	switch {
	case changed("no-mail"):
		v := !f.disabled
		o.Enabled = &v
		if f.disabled {
			falseVal := false
			o.Enabled = &falseVal
		}
	case changed("mail-enabled"):
		v := f.enabled
		o.Enabled = &v
	}

	if changed("mail-to") {
		o.To = f.to
	}
	if changed("mail-cc") {
		o.CC = f.cc
	}
	if changed("mail-bcc") {
		o.BCC = f.bcc
	}
	if changed("mail-from") {
		o.From = &f.from
	}
	if changed("mail-subject") {
		o.Subject = &f.subject
	}
	if changed("mail-smtp-host") {
		o.SMTPHost = &f.smtpHost
	}
	if changed("mail-smtp-port") {
		o.SMTPPort = &f.smtpPort
	}
	if changed("mail-smtp-user") {
		o.SMTPUser = &f.smtpUser
	}
	if changed("mail-smtp-pass") {
		o.SMTPPass = &f.smtpPass
	}
	if changed("mail-template") {
		o.TemplateFile = &f.template
	}
	if changed("mail-attach-log") {
		v := f.attachLog
		o.AttachLog = &v
	}
	if changed("mail-on") {
		o.On = f.on
	}
	return o
}

// mailRegistry tracks the email status of every scenario plus the in-flight
// send goroutines so program exit can wait for them. It also owns the
// per-scenario progress-ticker cancel functions so a removed scenario or
// a terminal-state transition can stop its ticker. Safe for concurrent
// use; the TUI reads Status snapshots while the engine watchers write
// transitions.
type mailRegistry struct {
	mu       sync.Mutex
	statuses map[*engine.Runner]*mail.Status
	progress map[*engine.Runner]context.CancelFunc
	sends    []<-chan struct{}
}

func newMailRegistry() *mailRegistry {
	return &mailRegistry{
		statuses: map[*engine.Runner]*mail.Status{},
		progress: map[*engine.Runner]context.CancelFunc{},
	}
}

// startProgress registers cancel as the in-flight progress ticker for r,
// cancelling any previously-registered one. This lets Restart spawn a
// fresh ticker without leaking the old goroutine.
func (m *mailRegistry) startProgress(r *engine.Runner, cancel context.CancelFunc) {
	m.mu.Lock()
	if old, ok := m.progress[r]; ok {
		old()
	}
	m.progress[r] = cancel
	m.mu.Unlock()
}

// stopProgress cancels and forgets the progress ticker for r if one is
// registered. No-op when nothing was running — safe to call at any
// lifecycle point including final cleanup.
func (m *mailRegistry) stopProgress(r *engine.Runner) {
	m.mu.Lock()
	if cancel, ok := m.progress[r]; ok {
		cancel()
		delete(m.progress, r)
	}
	m.mu.Unlock()
}

// StatusFor returns the status object for r, creating one in the Disabled
// state on first call. The returned pointer is stable for the lifetime of
// the registry — the TUI keeps a reference to poll.
func (m *mailRegistry) StatusFor(r *engine.Runner) *mail.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.statuses[r]; ok {
		return s
	}
	s := &mail.Status{}
	s.MarkDisabled()
	m.statuses[r] = s
	return s
}

// track records a send-completion channel so the program-exit path can
// wait on every outstanding goroutine.
func (m *mailRegistry) track(done <-chan struct{}) {
	m.mu.Lock()
	m.sends = append(m.sends, done)
	m.mu.Unlock()
}

// WaitAll blocks until every tracked send finishes or the timeout elapses.
// Called on program exit to give in-flight emails a chance to deliver.
func (m *mailRegistry) WaitAll(timeout time.Duration) {
	m.mu.Lock()
	channels := append([]<-chan struct{}{}, m.sends...)
	m.mu.Unlock()
	if len(channels) == 0 {
		return
	}
	deadline := time.After(timeout)
	for _, ch := range channels {
		select {
		case <-ch:
		case <-deadline:
			return
		}
	}
}

// AllStatuses returns a copy of the runner-to-status map. Used by the TUI
// to bootstrap its view of email state for newly-added scenarios.
func (m *mailRegistry) AllStatuses() map[*engine.Runner]*mail.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[*engine.Runner]*mail.Status, len(m.statuses))
	for k, v := range m.statuses {
		cp[k] = v
	}
	return cp
}

// wireScenarioMail merges the scenario's [email] block with the global
// mail-config file and CLI overrides, validates the result, and installs
// the OnTerminal callback that builds the message and dispatches it via
// mail.Sender.SendAsync.
//
// scenarioCfg may be nil (no [email] block in the scenario). globalFile
// is the parsed --mail-config or a zero Config. globalFileErr is the
// error from loading --mail-config (nil when load succeeded or the flag
// was absent). overrides is the CLI flag layer. logFile is the --log-file
// path used to attach run logs.
//
// Returns the (always non-nil) status pointer the TUI should poll for
// this runner. Configuration errors — bad SMTP config, missing
// --mail-config file, broken template — are surfaced as status.Failed
// rather than terminating the run, so a single bad scenario doesn't take
// down the whole dashboard. The Overview tab shows the error verbatim.
func wireScenarioMail(
	registry *mailRegistry,
	loaded *config.LoadedScenario,
	runner *engine.Runner,
	globalFile mail.Config,
	globalFileErr error,
	overrides mail.Overrides,
	logFile string,
) (*mail.Status, error) {
	var scenarioCfg mail.Config
	if loaded.Config.Email != nil {
		scenarioCfg = *loaded.Config.Email
	}
	merged := mail.Merge(scenarioCfg, globalFile, overrides)
	status := registry.StatusFor(runner)

	// If the user pointed --mail-config at a path that didn't load, this
	// is almost certainly a configuration mistake. Surface it as a Failed
	// status for every scenario where the user intended email — that
	// includes scenarios whose own [email] block enables it AND any
	// invocation where the CLI implies email (--mail-* flags). The same
	// applies when --mail-config produced no usable settings yet the
	// merged result still asks to send.
	if globalFileErr != nil && wantsEmail(scenarioCfg, overrides, merged) {
		status.MarkFailed(fmt.Errorf("--mail-config: %w", globalFileErr))
		return status, nil
	}

	if !merged.Enabled {
		// Email isn't on for this scenario — leave the status as
		// Disabled so the TUI hides the row.
		return status, nil
	}
	if err := merged.Validate(); err != nil {
		// Validation errors (missing host, no recipients, unknown trigger)
		// are user-visible config bugs. Surface in the TUI rather than
		// killing the run; other scenarios may still be valid.
		status.MarkFailed(fmt.Errorf("invalid email config: %w", err))
		return status, nil
	}

	// Resolve both body templates once at wiring time so a bad path
	// fails fast rather than at the moment the email tries to send. The
	// HTML template is optional; the text one always has at least the
	// built-in default. Same treatment as Validate: Failed status, not a
	// fatal error.
	textTmpl, htmlTmpl, err := mail.ResolveBodies(merged)
	if err != nil {
		status.MarkFailed(fmt.Errorf("body template: %w", err))
		return status, nil
	}

	// Promote the status out of Disabled so the Overview tab can show
	// "what's wired up" — triggers + progress cadence — before any
	// lifecycle event actually fires.
	status.MarkConfigured(merged.Triggers(), merged.ReportInterval.Duration)

	sender := mail.NewSender(merged)
	// startedAt is the wall-clock moment of the first start/restart. It
	// is rewritten by the OnStart handler on every fresh start so the
	// Elapsed shown in emails always reflects the *current* run rather
	// than the lifetime of the process.
	var startedAt = time.Now()

	// sendOneEmail renders both bodies, builds the message, and dispatches
	// async. Captured as a closure so the lifecycle handlers below
	// (OnStart, OnTerminal, progress ticker) all use the same code path —
	// only the trigger name differs.
	sendOneEmail := func(trigger string, state engine.State) {
		ctx := buildTemplateContext(runner, state, trigger, startedAt, time.Now())

		renderedText, rerr := mail.RenderText(textTmpl, ctx)
		if rerr != nil {
			status.MarkFailed(fmt.Errorf("render text body (%s): %w", trigger, rerr))
			return
		}
		var renderedHTML string
		if htmlTmpl != "" {
			renderedHTML, rerr = mail.RenderText(htmlTmpl, ctx)
			if rerr != nil {
				status.MarkFailed(fmt.Errorf("render html body (%s): %w", trigger, rerr))
				return
			}
		}
		subject := mail.RenderSubjectWithFallback(merged.Subject, ctx)

		msg := mail.Message{
			From:     merged.From,
			To:       merged.To,
			CC:       merged.CC,
			BCC:      merged.BCC,
			Subject:  subject,
			TextBody: renderedText,
			HTMLBody: renderedHTML,
		}
		if merged.AttachLog {
			msg.Attachments = buildLogAttachments(runner, logFile)
		}
		status.SetTrigger(trigger)
		done := sender.SendAsync(msg, status)
		registry.track(done)
	}

	// Lifecycle callback: fired on IDLE → RUNNING via Start() / Restart().
	// Sends the "start" email when configured and arms the progress
	// ticker. Both are no-ops if the user hasn't asked for those
	// triggers; ticker is also a no-op when report_interval is zero.
	runner.SetOnStart(func() {
		startedAt = time.Now()

		if merged.FiresOn("start") {
			sendOneEmail("start", engine.StateRunning)
		}

		if merged.FiresOn("progress") && merged.ReportInterval.Duration > 0 {
			ctx, cancel := context.WithCancel(context.Background())
			// Register the cancel BEFORE spawning the goroutine so a
			// concurrent OnTerminal can't race us by completing before
			// the registry knows about the ticker.
			registry.startProgress(runner, cancel)
			go runProgressTicker(ctx, runner, merged.ReportInterval.Duration, sendOneEmail)
		}
	})

	// Lifecycle callback: fired when the runner reaches a terminal state.
	// Cancels any in-flight progress ticker so a scenario that finishes
	// before its next report tick doesn't emit a stale "progress" email
	// after the "done" email.
	//
	// The engine itself only emits StateDone today — the StateError
	// constant exists but no engine code path assigns it. We therefore
	// pick "done" vs "error" here from the final snapshot: when the
	// scenario completed with errors that dominate (more errors than
	// successful replies) we classify it as ERROR. With on=["done",
	// "error"] exactly one of the two fires, matching the outcome.
	runner.SetOnTerminal(func(state engine.State) {
		registry.stopProgress(runner)
		trigger := chooseTerminalTrigger(runner, state)
		if trigger == "" || !merged.FiresOn(trigger) {
			return
		}
		sendOneEmail(trigger, state)
	})

	// Flip the status from Disabled to ... well, still Disabled until the
	// send actually fires. We don't have a "configured but not yet sent"
	// state on purpose — the TUI shows nothing until the goroutine starts.
	return status, nil
}

// wantsEmail reports whether the user gave any indication that email
// should be sent for this scenario. Used to decide whether a failure to
// load --mail-config is surface-worthy: if the user didn't ask for mail
// anywhere, a missing file isn't worth surfacing in the TUI.
//
// We treat ANY mail-related CLI flag (other than --no-mail, which is the
// opposite intent) as "the user wants mail". This is intentionally broad
// so a typo in the config path always reaches the user's eyes.
func wantsEmail(scenarioCfg mail.Config, overrides mail.Overrides, merged mail.Config) bool {
	if scenarioCfg.Enabled || merged.Enabled {
		return true
	}
	// --mail-enabled, --mail-to, --mail-from, --mail-smtp-* etc all imply
	// intent. --no-mail explicitly opts out — respect it.
	if overrides.Enabled != nil {
		return *overrides.Enabled
	}
	if len(overrides.To) > 0 || len(overrides.CC) > 0 || len(overrides.BCC) > 0 {
		return true
	}
	if overrides.From != nil || overrides.Subject != nil ||
		overrides.SMTPHost != nil || overrides.SMTPPort != nil ||
		overrides.SMTPUser != nil || overrides.SMTPPass != nil ||
		overrides.Template != nil || overrides.TemplateFile != nil ||
		overrides.AttachLog != nil || len(overrides.On) > 0 {
		return true
	}
	return false
}

// triggerNameForState maps an engine state to the lowercase trigger name
// used in [email] on = […]. Anything other than the two terminal states
// returns the empty string (no fire).
func triggerNameForState(s engine.State) string {
	switch s {
	case engine.StateDone:
		return "done"
	case engine.StateError:
		return "error"
	}
	return ""
}

// chooseTerminalTrigger picks the lifecycle event name to fire when the
// runner reaches a terminal state. The engine currently only ever sets
// StateDone, so the choice between "done" and "error" comes from the
// final snapshot rather than the state enum: when the run's error count
// dominates we classify it as ERROR. The threshold (errors strictly
// greater than received) is conservative — a few sporadic failures stay
// classified as "done", but a scenario where most or all requests
// failed surfaces as "error". Returns "" when the state isn't terminal
// (defensive — the runner only invokes OnTerminal in terminal states).
func chooseTerminalTrigger(runner *engine.Runner, state engine.State) string {
	if name := triggerNameForState(state); name == "error" {
		return name
	} else if name == "" {
		return ""
	}
	snap := runner.Snapshot()
	if snap.Errors > 0 && snap.Errors > snap.Received {
		return "error"
	}
	return "done"
}

// initialProgressDelay is the cap on how long we wait for the first
// "progress" email. A user who configures a long report_interval (say
// "5m") almost certainly still wants to know a quick scenario fired —
// without this, a scenario that completes in 10 seconds would never
// emit a progress mail despite the user opting in. So the first tick
// fires at min(interval, initialProgressDelay); subsequent ticks
// follow the user's configured interval as documented.
const initialProgressDelay = 5 * time.Second

// runProgressTicker fires "progress" emails for the lifetime of a
// scenario run.
//
// Timing:
//   - First fire:    min(interval, initialProgressDelay) after start.
//                    Ensures short scenarios still see one progress mail.
//   - Subsequent:    every interval, on a stock time.Ticker.
//
// Cancellation: ctx.Done returns the goroutine immediately. The
// registry's stopProgress(runner) call (issued by OnTerminal or by
// scenario removal cleanup) is what cancels ctx, so a run that finishes
// between ticks never produces a stray progress mail after its
// done/error mail.
//
// State check: ticks fire only while runner.State() == RUNNING. A
// scenario paused with Stop silently skips ticks; Resume picks them
// back up without us having to tear down and rebuild the ticker.
func runProgressTicker(ctx context.Context, runner *engine.Runner, interval time.Duration, send func(string, engine.State)) {
	firstDelay := interval
	if firstDelay > initialProgressDelay {
		firstDelay = initialProgressDelay
	}

	// Initial fire — completed once, then we fall into the interval loop.
	select {
	case <-ctx.Done():
		return
	case <-time.After(firstDelay):
	}
	if runner.State() == engine.StateRunning {
		send("progress", engine.StateRunning)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if runner.State() != engine.StateRunning {
				continue
			}
			send("progress", engine.StateRunning)
		}
	}
}

// buildTemplateContext snapshots the runner's current state into a value
// suitable for both subject and body rendering. trigger is the lifecycle
// event that prompted the send ("start" / "progress" / "done" / "error")
// and is propagated verbatim to TemplateContext.Trigger so templates can
// branch on the reason for the email.
func buildTemplateContext(runner *engine.Runner, state engine.State, trigger string, startedAt, finishedAt time.Time) mail.TemplateContext {
	snap := runner.Snapshot()

	latency := map[string]string{}
	for k, v := range snap.Percentiles {
		latency[fmt.Sprintf("p%g", k)] = metrics.FormatLatency(v)
	}

	ctx := mail.TemplateContext{
		Scenario: mail.ScenarioInfo{
			Name:        runner.Name(),
			Description: runner.Description(),
		},
		State:        state.String(),
		Trigger:      trigger,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Elapsed:      formatElapsed(finishedAt.Sub(startedAt)),
		Sent:         snap.Sent,
		Received:     snap.Received,
		Errors:       snap.Errors,
		MsgPerSec:    snap.MsgPerSec,
		MaxMsgPerSec: snap.MaxMsgPerSec,
		AvgMsgPerSec: snap.AvgMsgPerSec,
		Latency:      latency,
		Predicates:   buildPredicateRows(snap),
	}
	return ctx
}

// buildPredicateRows converts the snapshot's predicate map into the
// ordered slice the template expects. Sorted by name for stable output.
func buildPredicateRows(snap metrics.Snapshot) []mail.PredicateRow {
	if len(snap.Predicates) == 0 {
		return nil
	}
	var total int64
	for _, p := range snap.Predicates {
		total += p.Count
	}
	rows := make([]mail.PredicateRow, 0, len(snap.Predicates))
	for name, p := range snap.Predicates {
		var pct float64
		if total > 0 {
			pct = 100.0 * float64(p.Count) / float64(total)
		}
		rows = append(rows, mail.PredicateRow{
			Name:  name,
			Count: p.Count,
			Pct:   pct,
			P50:   metrics.FormatLatency(p.Percentiles[50]),
			P95:   metrics.FormatLatency(p.Percentiles[95]),
			P99:   metrics.FormatLatency(p.Percentiles[99]),
		})
	}
	// Stable order: name alphabetical. The collector returns a map so
	// without sorting the template would shuffle each invocation.
	sortPredicateRows(rows)
	return rows
}

// sortPredicateRows sorts in place by name. Inline tiny sort to avoid
// pulling sort.SliceStable when the caller is the one place that needs it.
func sortPredicateRows(rows []mail.PredicateRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j-1].Name > rows[j].Name; j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// formatElapsed renders a duration as HH:MM:SS for use in the Elapsed
// template field. Matches the format the TUI shows.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// buildLogAttachments collects the scenario's in-memory log lines and, if
// configured, the --log-file contents. Both go in as text/plain.
//
// We cap the log-file attachment at 1 MB to keep email bodies sane; if
// the file is larger we read the *tail* so the most recent (and usually
// most relevant) entries survive, with a marker line at the top.
func buildLogAttachments(runner *engine.Runner, logFile string) []mail.Attachment {
	var out []mail.Attachment

	lines := runner.LogTail(0)
	if len(lines) > 0 {
		content := strings.Join(lines, "\n") + "\n"
		out = append(out, mail.Attachment{
			Name:    safeAttachmentName(runner.Name()) + ".log",
			Content: []byte(content),
		})
	}

	if logFile != "" {
		data, err := readLogFileTail(logFile, 1024*1024)
		if err == nil && len(data) > 0 {
			out = append(out, mail.Attachment{
				Name:    "run.log",
				Content: data,
			})
		}
	}
	return out
}

// safeAttachmentName sanitises a scenario name into a value safe to embed
// in a Content-Disposition filename. Conservative: only [A-Za-z0-9_-].
func safeAttachmentName(s string) string {
	if s == "" {
		return "scenario"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// readLogFileTail returns the last maxBytes of path, with a leading
// marker line when the file was larger than the cap.
func readLogFileTail(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := stat.Size()
	if size <= maxBytes {
		data, err := os.ReadFile(path)
		return data, err
	}
	if _, err := f.Seek(size-maxBytes, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil {
		return nil, err
	}
	marker := fmt.Sprintf("(log truncated to last %d bytes of %d)\n", maxBytes, size)
	return append([]byte(marker), buf[:n]...), nil
}
