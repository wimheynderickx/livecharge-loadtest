package mail

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// LoadFile reads a TOML file as a Config. Used for --mail-config; the
// whole file is the email config (no [email] header).
//
// An empty path returns an empty Config and no error so callers can call
// it unconditionally.
func LoadFile(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read mail config %s: %w", path, err)
	}
	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse mail config %s: %w", path, err)
	}
	return cfg, nil
}

// Overrides carries the CLI-flag values for the email feature. Each
// pointer-shaped field is nil when the user didn't pass the corresponding
// flag, so we can distinguish "not set" from "explicitly set to the zero
// value" (e.g. --mail-attach-log=false).
//
// Slice fields use the empty slice as "not set" — there is no Go-idiomatic
// way to pass "explicitly empty" on the command line and the convention
// matches how cobra/pflag represent string-slice defaults.
type Overrides struct {
	Enabled      *bool
	SendTimeout  *Duration
	AttachLog    *bool
	SMTPHost     *string
	SMTPPort     *int
	SMTPUser     *string
	SMTPPass     *string
	From         *string
	To           []string
	CC           []string
	BCC          []string
	Subject      *string
	Template     *string
	TemplateFile *string
	On           []string
}

// Merge composes the three config sources into a single resolved Config,
// following the user's precedence rule: scenario [email] is the floor,
// --mail-config overrides per-field, CLI overrides everything per-field.
//
// Per-field merging is deliberate: a user can put credentials in a shared
// file and pass --mail-subject on the command line without losing the rest.
//
// Exception: report_interval. Progress-mail cadence is scenario-specific
// (soak vs smoke tests want very different intervals), so the scenario's
// report_interval wins over the file. The file only fills in when the
// scenario didn't set one. CLI still wins over both, as for every field.
//
// Side effects: ApplyDefaults runs on the final result so callers don't
// have to remember it.
func Merge(scenarioBlock, fileConfig Config, overrides Overrides) Config {
	out := scenarioBlock
	mergeFromFile(&out, fileConfig)
	mergeFromOverrides(&out, overrides)
	// LOADTEST_SMTP_PASS env var fills SMTPPass when nothing else did.
	// This is the standard "secret in env, everything else in TOML" UX.
	if out.SMTPPass == "" {
		if v := os.Getenv("LOADTEST_SMTP_PASS"); v != "" {
			out.SMTPPass = v
		}
	}
	out.ApplyDefaults()
	return out
}

// mergeFromFile copies non-zero fields from src into dst. We treat empty
// strings, zero ints, nil slices, and zero Durations as "not set" — the
// user opted out by leaving the field absent from the TOML.
//
// Enabled and AttachLog are bools so the zero value (false) is ambiguous.
// We follow the convention "if anything in src is non-zero, treat the bool
// as authoritative too" — i.e. having a [email] block at all in the
// mail-config file means its enabled/attach_log values count. This is the
// least surprising behaviour for an admin maintaining a shared file.
func mergeFromFile(dst *Config, src Config) {
	if isFilePopulated(src) {
		dst.Enabled = src.Enabled
		dst.AttachLog = src.AttachLog
	}
	if len(src.On) > 0 {
		dst.On = src.On
	}
	if src.SendTimeout.Duration > 0 {
		dst.SendTimeout = src.SendTimeout
	}
	// report_interval is the one field where the scenario block wins over
	// the mail-config file: progress cadence is scenario-specific (a soak
	// test wants minutes, a smoke test wants seconds) and the shared file
	// only acts as a fallback when the scenario didn't set one. Every
	// other field follows the documented "mail-config overrides scenario"
	// rule.
	if src.ReportInterval.Duration > 0 && dst.ReportInterval.Duration == 0 {
		dst.ReportInterval = src.ReportInterval
	}
	if src.SMTPHost != "" {
		dst.SMTPHost = src.SMTPHost
	}
	if src.SMTPPort != 0 {
		dst.SMTPPort = src.SMTPPort
	}
	if src.SMTPUser != "" {
		dst.SMTPUser = src.SMTPUser
	}
	if src.SMTPPass != "" {
		dst.SMTPPass = src.SMTPPass
	}
	if src.From != "" {
		dst.From = src.From
	}
	if len(src.To) > 0 {
		dst.To = src.To
	}
	if len(src.CC) > 0 {
		dst.CC = src.CC
	}
	if len(src.BCC) > 0 {
		dst.BCC = src.BCC
	}
	if src.Subject != "" {
		dst.Subject = src.Subject
	}
	if src.Template != "" {
		dst.Template = src.Template
	}
	if src.TemplateFile != "" {
		dst.TemplateFile = src.TemplateFile
	}
	if src.TemplateHTML != "" {
		dst.TemplateHTML = src.TemplateHTML
	}
	if src.TemplateFileHTML != "" {
		dst.TemplateFileHTML = src.TemplateFileHTML
	}
}

// isFilePopulated reports whether src looks like it came from a real
// TOML file as opposed to a zero-value placeholder. Used to decide
// whether the bool fields should be authoritative.
func isFilePopulated(src Config) bool {
	return src.SMTPHost != "" || src.From != "" || len(src.To) > 0 ||
		len(src.CC) > 0 || len(src.BCC) > 0 || src.Subject != "" ||
		src.Template != "" || src.TemplateFile != "" || src.SMTPUser != ""
}

// mergeFromOverrides copies CLI flag values into dst. Pointer fields are
// only applied when non-nil; slices when non-empty. This is the strictest
// "explicit" rule because the user typed the flag.
func mergeFromOverrides(dst *Config, o Overrides) {
	if o.Enabled != nil {
		dst.Enabled = *o.Enabled
	}
	if o.SendTimeout != nil && o.SendTimeout.Duration > 0 {
		dst.SendTimeout = *o.SendTimeout
	}
	if o.AttachLog != nil {
		dst.AttachLog = *o.AttachLog
	}
	if o.SMTPHost != nil {
		dst.SMTPHost = *o.SMTPHost
	}
	if o.SMTPPort != nil {
		dst.SMTPPort = *o.SMTPPort
	}
	if o.SMTPUser != nil {
		dst.SMTPUser = *o.SMTPUser
	}
	if o.SMTPPass != nil {
		dst.SMTPPass = *o.SMTPPass
	}
	if o.From != nil {
		dst.From = *o.From
	}
	if len(o.To) > 0 {
		dst.To = o.To
	}
	if len(o.CC) > 0 {
		dst.CC = o.CC
	}
	if len(o.BCC) > 0 {
		dst.BCC = o.BCC
	}
	if o.Subject != nil {
		dst.Subject = *o.Subject
	}
	if o.Template != nil {
		dst.Template = *o.Template
	}
	if o.TemplateFile != nil {
		dst.TemplateFile = *o.TemplateFile
	}
	if len(o.On) > 0 {
		dst.On = o.On
	}
}

// ResolveBodies returns the body templates the user configured:
//
//   - textTmpl  → plain-text source. Always non-empty: when the user
//                 set neither template/template_file the built-in default
//                 fills in.
//   - htmlTmpl  → HTML source, or "" when no html template was set.
//
// Reads from disk for the *File variants so callers don't have to.
//
// This is the single source of truth for "which bodies will the email
// carry"; the sender derives its MIME structure from whether htmlTmpl
// is empty:
//
//   - htmlTmpl != "" + textTmpl set  → multipart/alternative
//   - htmlTmpl == ""                  → single text/plain part
//   - textTmpl == "" + htmlTmpl != "" → single text/html part
//                                       (textTmpl always populated, so
//                                       this branch only triggers when a
//                                       future caller explicitly opts out
//                                       of the text fallback)
func ResolveBodies(cfg Config) (textTmpl, htmlTmpl string, err error) {
	switch {
	case cfg.TemplateFile != "":
		data, rerr := os.ReadFile(cfg.TemplateFile)
		if rerr != nil {
			return "", "", fmt.Errorf("read template_file %s: %w", cfg.TemplateFile, rerr)
		}
		textTmpl = string(data)
	case cfg.Template != "":
		textTmpl = cfg.Template
	default:
		textTmpl = DefaultBodyTemplate
	}

	switch {
	case cfg.TemplateFileHTML != "":
		data, rerr := os.ReadFile(cfg.TemplateFileHTML)
		if rerr != nil {
			return "", "", fmt.Errorf("read template_file_html %s: %w", cfg.TemplateFileHTML, rerr)
		}
		htmlTmpl = string(data)
	case cfg.TemplateHTML != "":
		htmlTmpl = cfg.TemplateHTML
	}
	return textTmpl, htmlTmpl, nil
}
