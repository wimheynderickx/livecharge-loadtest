package mail

import (
	"strings"
	"testing"
	"time"
)

func TestConfig_ValidateDisabledAlwaysOK(t *testing.T) {
	// A disabled config with no other fields set must not error — we
	// want users to be able to write a placeholder [email] block.
	c := Config{Enabled: false}
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled config: unexpected error %v", err)
	}
}

func TestConfig_ValidateRequiresHostFromAndRecipient(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		errSub string
	}{
		{"missing host", Config{Enabled: true, From: "a@b", To: []string{"c@d"}}, "smtp_host"},
		{"missing from", Config{Enabled: true, SMTPHost: "h", To: []string{"c@d"}}, "from"},
		{"no recipient", Config{Enabled: true, SMTPHost: "h", From: "a@b"}, "recipient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.errSub)
			}
		})
	}
}

func TestConfig_ValidateRejectsUnknownTrigger(t *testing.T) {
	c := Config{
		Enabled:  true,
		SMTPHost: "h", From: "f@x", To: []string{"a@b"},
		On: []string{"done", "midway"},
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "midway") {
		t.Fatalf("expected unknown-trigger error, got %v", err)
	}
}

func TestConfig_ValidateAcceptsStartAndProgressTriggers(t *testing.T) {
	c := Config{
		Enabled:  true,
		SMTPHost: "h", From: "f@x", To: []string{"a@b"},
		On:             []string{"start", "progress", "done", "error"},
		ReportInterval: Duration{30 * time.Second},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("all four triggers should be accepted: %v", err)
	}
}

func TestConfig_ValidateProgressRequiresReportInterval(t *testing.T) {
	c := Config{
		Enabled:  true,
		SMTPHost: "h", From: "f@x", To: []string{"a@b"},
		On: []string{"progress"}, // no report_interval
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "report_interval") {
		t.Fatalf("expected report_interval error, got %v", err)
	}

	// Setting report_interval makes the same config valid.
	c.ReportInterval = Duration{10 * time.Second}
	if err := c.Validate(); err != nil {
		t.Fatalf("config should pass with report_interval set: %v", err)
	}
}

func TestConfig_TriggersDefaults(t *testing.T) {
	c := Config{}
	got := c.Triggers()
	if len(got) != 2 || got[0] != "done" || got[1] != "error" {
		t.Fatalf("default triggers should be [done, error], got %v", got)
	}
}

func TestConfig_FiresOn(t *testing.T) {
	c := Config{On: []string{" DONE "}}
	if !c.FiresOn("done") {
		t.Fatalf("case + whitespace should match")
	}
	if c.FiresOn("error") {
		t.Fatalf("should not fire on error when only done configured")
	}
}

func TestConfig_TimeoutDefault(t *testing.T) {
	c := Config{}
	if c.Timeout() != DefaultSendTimeout {
		t.Fatalf("default timeout should be %v, got %v", DefaultSendTimeout, c.Timeout())
	}
	c.SendTimeout = Duration{5 * time.Second}
	if c.Timeout() != 5*time.Second {
		t.Fatalf("explicit timeout not respected: %v", c.Timeout())
	}
}

func TestConfig_ApplyDefaults(t *testing.T) {
	c := Config{}
	c.ApplyDefaults()
	if c.From != DefaultFrom {
		t.Errorf("From default not applied: %q", c.From)
	}
	if c.Subject != DefaultSubject {
		t.Errorf("Subject default not applied: %q", c.Subject)
	}
	if c.SMTPPort != DefaultSMTPPort {
		t.Errorf("SMTPPort default not applied: %d", c.SMTPPort)
	}

	// User-set values should be preserved.
	c = Config{From: "x@y", Subject: "S", SMTPPort: 25}
	c.ApplyDefaults()
	if c.From != "x@y" || c.Subject != "S" || c.SMTPPort != 25 {
		t.Errorf("ApplyDefaults clobbered user values: %+v", c)
	}
}

func TestDuration_UnmarshalText(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("45s")); err != nil {
		t.Fatal(err)
	}
	if d.Duration != 45*time.Second {
		t.Fatalf("got %v, want 45s", d.Duration)
	}
	// Empty input is a no-op
	d = Duration{}
	if err := d.UnmarshalText([]byte("")); err != nil {
		t.Fatal(err)
	}
	// Garbage should error
	if err := d.UnmarshalText([]byte("nope")); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}
