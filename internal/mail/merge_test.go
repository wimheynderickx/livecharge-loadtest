package mail

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ptrStr(s string) *string       { return &s }
func ptrBool(b bool) *bool          { return &b }
func ptrInt(n int) *int             { return &n }
func ptrDur(d time.Duration) *Duration { v := Duration{d}; return &v }

func TestMerge_ScenarioOnly(t *testing.T) {
	// Scenario block is the floor — when no file or CLI is provided, its
	// values flow through unchanged plus defaults.
	scenario := Config{
		Enabled:  true,
		SMTPHost: "smtp.example.com",
		From:     "alice@example.com",
		To:       []string{"bob@example.com"},
	}
	out := Merge(scenario, Config{}, Overrides{})
	if out.SMTPHost != "smtp.example.com" || out.From != "alice@example.com" {
		t.Fatalf("scenario values lost: %+v", out)
	}
	if out.SMTPPort != DefaultSMTPPort {
		t.Errorf("default port not applied: %d", out.SMTPPort)
	}
}

func TestMerge_FileOverridesScenario(t *testing.T) {
	// Per-field override: the file changes host and recipients but the
	// scenario's From should remain unchanged because the file doesn't set it.
	scenario := Config{
		Enabled:  true,
		SMTPHost: "scenario-host",
		From:     "scenario-from@x",
		To:       []string{"scenario-to@x"},
	}
	file := Config{
		SMTPHost: "file-host",
		To:       []string{"file-to@x"},
	}
	out := Merge(scenario, file, Overrides{})
	if out.SMTPHost != "file-host" {
		t.Errorf("file host should override scenario, got %q", out.SMTPHost)
	}
	if out.From != "scenario-from@x" {
		t.Errorf("scenario From should be preserved when file doesn't set it, got %q", out.From)
	}
	if len(out.To) != 1 || out.To[0] != "file-to@x" {
		t.Errorf("file To should override scenario, got %v", out.To)
	}
}

func TestMerge_CLIOverridesEverything(t *testing.T) {
	scenario := Config{Enabled: true, SMTPHost: "scenario", From: "scen@x", To: []string{"x@y"}}
	file := Config{SMTPHost: "file"}
	overrides := Overrides{
		SMTPHost: ptrStr("cli-host"),
		Subject:  ptrStr("cli-subject"),
	}
	out := Merge(scenario, file, overrides)
	if out.SMTPHost != "cli-host" {
		t.Errorf("CLI host did not win: %q", out.SMTPHost)
	}
	if out.Subject != "cli-subject" {
		t.Errorf("CLI subject did not win: %q", out.Subject)
	}
	// Untouched fields fall through.
	if out.From != "scen@x" {
		t.Errorf("From should fall through from scenario: %q", out.From)
	}
}

func TestMerge_CLIBoolsExplicitlyDisable(t *testing.T) {
	// --no-mail should turn off a scenario that has it enabled.
	scenario := Config{Enabled: true, SMTPHost: "h", From: "a@b", To: []string{"c@d"}}
	out := Merge(scenario, Config{}, Overrides{Enabled: ptrBool(false)})
	if out.Enabled {
		t.Fatalf("CLI --no-mail should disable, got enabled=true")
	}
}

func TestMerge_EnvVarFillsSMTPPass(t *testing.T) {
	t.Setenv("LOADTEST_SMTP_PASS", "from-env")
	out := Merge(Config{Enabled: true, SMTPHost: "h", From: "a", To: []string{"b"}}, Config{}, Overrides{})
	if out.SMTPPass != "from-env" {
		t.Fatalf("env var should fill SMTPPass, got %q", out.SMTPPass)
	}

	// But an explicit pass beats the env var.
	out = Merge(Config{SMTPPass: "scen-pass"}, Config{}, Overrides{})
	if out.SMTPPass != "scen-pass" {
		t.Fatalf("scenario pass should beat env, got %q", out.SMTPPass)
	}
}

func TestLoadFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail-config.toml")
	body := `
enabled    = true
on         = ["done"]
smtp_host  = "relay.example.com"
smtp_port  = 587
smtp_user  = "user"
smtp_pass  = "pw"
from       = "Test <test@x.com>"
to         = ["a@b.com", "c@d.com"]
subject    = "ran"
attach_log = true
send_timeout = "15s"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.SMTPHost != "relay.example.com" {
		t.Errorf("SMTPHost: %q", cfg.SMTPHost)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("SMTPPort: %d", cfg.SMTPPort)
	}
	if cfg.SendTimeout.Duration != 15*time.Second {
		t.Errorf("SendTimeout: %v", cfg.SendTimeout.Duration)
	}
	if len(cfg.To) != 2 {
		t.Errorf("To: %v", cfg.To)
	}
	if !cfg.AttachLog {
		t.Errorf("AttachLog should be true")
	}
}

func TestLoadFile_EmptyPath(t *testing.T) {
	cfg, err := LoadFile("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.SMTPHost != "" {
		t.Fatalf("empty path should return zero config, got %+v", cfg)
	}
}

func TestResolveBodies_DefaultWhenUnset(t *testing.T) {
	text, html, err := ResolveBodies(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if text != DefaultBodyTemplate {
		t.Fatalf("expected default text template, got %q", text[:40])
	}
	if html != "" {
		t.Fatalf("expected no html template, got %q", html)
	}
}

func TestResolveBodies_InlineText(t *testing.T) {
	text, html, err := ResolveBodies(Config{Template: "inline body"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "inline body" {
		t.Fatalf("expected inline body, got %q", text)
	}
	if html != "" {
		t.Fatalf("expected no html template, got %q", html)
	}
}

func TestResolveBodies_InlineHTML(t *testing.T) {
	text, html, err := ResolveBodies(Config{TemplateHTML: "<p>hi</p>"})
	if err != nil {
		t.Fatal(err)
	}
	// Text defaults to built-in so multipart/alternative gets a fallback.
	if text != DefaultBodyTemplate {
		t.Fatalf("expected default text fallback, got %q", text[:40])
	}
	if html != "<p>hi</p>" {
		t.Fatalf("expected inline html, got %q", html)
	}
}

func TestResolveBodies_BothFromFiles(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "body.txt")
	htmlPath := filepath.Join(dir, "body.html")
	if err := os.WriteFile(textPath, []byte("file text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(htmlPath, []byte("<p>file html</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, html, err := ResolveBodies(Config{TemplateFile: textPath, TemplateFileHTML: htmlPath})
	if err != nil {
		t.Fatal(err)
	}
	if text != "file text" || html != "<p>file html</p>" {
		t.Fatalf("got text=%q html=%q", text, html)
	}
}

func TestResolveBodies_MissingTextFile(t *testing.T) {
	_, _, err := ResolveBodies(Config{TemplateFile: "/no/such/path"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
