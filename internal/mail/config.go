package mail

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config holds every setting for the email notification feature.
//
// It is loaded from three sources (scenario [email] block, --mail-config
// file, and CLI flags) and merged via Merge. After merging, call Validate
// before passing to a Sender — the merger leaves the struct in whatever
// half-populated state the inputs produced.
//
// The Has* fields exist so Merge can tell "explicitly set to zero" apart
// from "not set". The TOML tags use ',omitempty' for the bools so an
// absent key stays false rather than being decoded as a zero value the
// merger interprets as "yes, override". We track explicit-ness via a
// separate map populated in LoadFile, not via has-pointer wrapping, to
// keep the TOML schema flat and human-friendly.
type Config struct {
	// Enabled toggles the whole feature on or off. When false the
	// callback returns immediately and the TUI hides the email row.
	Enabled bool `toml:"enabled"`

	// On lists which lifecycle events trigger the email. Valid values:
	//
	//   - "start"    — fired once the scenario transitions IDLE → RUNNING.
	//                  Skipped on Resume (continuing a stopped scenario)
	//                  and skipped on the re-start half of a Restart's
	//                  stop/reset/start cycle when the prior run wasn't
	//                  IDLE — i.e. only "fresh starts" fire it.
	//   - "progress" — fired every ReportInterval while the scenario is
	//                  RUNNING. The ticker is cancelled as soon as the
	//                  scenario reaches a terminal state, so a fast run
	//                  that finishes before the first interval gets no
	//                  progress email.
	//   - "done"     — fired once on natural completion (total_messages or
	//                  duration reached).
	//   - "error"    — fired once on fatal transport failure.
	//
	// Empty means "use the default" — currently ["done", "error"]; we keep
	// the older two-trigger default so existing configs continue working
	// unchanged after start/progress were added.
	On []string `toml:"on"`

	// SendTimeout caps how long the goroutine waits for the SMTP dialogue
	// to complete. The parent process honours the same bound on shutdown.
	SendTimeout Duration `toml:"send_timeout"`

	// ReportInterval is the cadence for "progress" emails. Required (and
	// must be > 0) when "progress" is one of the configured triggers;
	// ignored otherwise. A 5-minute interval is a sensible default for
	// soak / overnight tests; tests measured in seconds shouldn't enable
	// progress mail at all.
	ReportInterval Duration `toml:"report_interval"`

	// AttachLog controls whether the scenario's in-memory log buffer and
	// the --log-file contents (when set) are attached to the email.
	AttachLog bool `toml:"attach_log"`

	// SMTP transport. Auth is sent over STARTTLS once the server advertises
	// it; if the server refuses STARTTLS and the host is not localhost the
	// sender aborts rather than transmit credentials in the clear.
	SMTPHost string `toml:"smtp_host"`
	SMTPPort int    `toml:"smtp_port"`
	SMTPUser string `toml:"smtp_user"`
	SMTPPass string `toml:"smtp_pass"`

	// Message envelope.
	From    string   `toml:"from"`
	To      []string `toml:"to"`
	CC      []string `toml:"cc"`
	BCC     []string `toml:"bcc"`
	Subject string   `toml:"subject"`

	// Body templates. Either, neither, or both may be set.
	//
	//   - Template / TemplateFile      → text/plain body
	//   - TemplateHTML / TemplateFileHTML → text/html body
	//
	// When both are configured, the email is sent as multipart/alternative
	// so each mail client renders the format it supports best — modern
	// clients show the HTML version, plain-text readers fall back to the
	// text one. When only one is set, that body is sent on its own. When
	// neither is set, the built-in plain-text default is used.
	//
	// The *File variants take precedence over inline strings when both
	// are provided (per format).
	Template         string `toml:"template"`
	TemplateFile     string `toml:"template_file"`
	TemplateHTML     string `toml:"template_html"`
	TemplateFileHTML string `toml:"template_file_html"`
}

// DefaultSendTimeout is the value used when none is configured.
const DefaultSendTimeout = 30 * time.Second

// DefaultFrom is the From header used when the user doesn't override it.
// "Livecharge OCS LoadTest" was specifically requested as the default
// display name.
const DefaultFrom = "Livecharge OCS LoadTest <noreply@livecharge.local>"

// DefaultSubject is used when no subject is configured.
const DefaultSubject = "[Livecharge] {{.Scenario.Name}} — {{.State}}"

// DefaultSMTPPort matches the modern STARTTLS submission port.
const DefaultSMTPPort = 587

// Duration is a TOML-friendly time.Duration that accepts strings like
// "30s" or "5m". time.Duration itself doesn't have a TOML unmarshaler so
// we wrap it. The zero value means "use DefaultSendTimeout".
type Duration struct {
	time.Duration
}

// UnmarshalText satisfies encoding.TextUnmarshaler so BurntSushi/toml can
// decode "30s" into a Duration.
func (d *Duration) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		return nil
	}
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	d.Duration = parsed
	return nil
}

// MarshalText is the inverse used when writing back to TOML in tests.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// Validate checks the merged config for the minimum set of fields needed
// to actually send. Returns nil when Enabled is false so we don't reject
// runs that just have an [email] block as a placeholder.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.SMTPHost == "" {
		return errors.New("mail: smtp_host is required when enabled")
	}
	if c.From == "" {
		return errors.New("mail: from is required when enabled")
	}
	if len(c.To) == 0 && len(c.CC) == 0 && len(c.BCC) == 0 {
		return errors.New("mail: at least one recipient (to/cc/bcc) is required")
	}
	hasProgress := false
	for _, trigger := range c.On {
		t := strings.ToLower(strings.TrimSpace(trigger))
		switch t {
		case "start", "progress", "done", "error":
			// ok
		default:
			return fmt.Errorf("mail: unknown trigger %q (allowed: start, progress, done, error)", trigger)
		}
		if t == "progress" {
			hasProgress = true
		}
	}
	if hasProgress && c.ReportInterval.Duration <= 0 {
		return fmt.Errorf("mail: report_interval must be > 0 when \"progress\" is in on")
	}
	return nil
}

// Triggers returns the lifecycle events that should fire the email,
// applying the default ("done" + "error") when On is empty. The returned
// slice is always lowercase and trimmed.
func (c *Config) Triggers() []string {
	if len(c.On) == 0 {
		return []string{"done", "error"}
	}
	out := make([]string, 0, len(c.On))
	for _, t := range c.On {
		out = append(out, strings.ToLower(strings.TrimSpace(t)))
	}
	return out
}

// FiresOn reports whether the given lifecycle event should send the email.
func (c *Config) FiresOn(event string) bool {
	want := strings.ToLower(event)
	for _, t := range c.Triggers() {
		if t == want {
			return true
		}
	}
	return false
}

// Timeout returns SendTimeout with the default applied for the zero value.
func (c *Config) Timeout() time.Duration {
	if c.SendTimeout.Duration <= 0 {
		return DefaultSendTimeout
	}
	return c.SendTimeout.Duration
}

// ApplyDefaults fills in From, Subject and SMTPPort when they are blank.
// Body templates are intentionally NOT defaulted here — ResolveBodies()
// decides what to send based on which templates the user populated, so
// "no template at all" produces the built-in plain-text body without us
// having to record that fact in the struct.
func (c *Config) ApplyDefaults() {
	if c.From == "" {
		c.From = DefaultFrom
	}
	if c.Subject == "" {
		c.Subject = DefaultSubject
	}
	if c.SMTPPort == 0 {
		c.SMTPPort = DefaultSMTPPort
	}
}
