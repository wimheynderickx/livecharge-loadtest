// Package mail implements the optional post-run email notification feature.
//
// When a scenario reaches DONE or ERROR, the engine fires an OnTerminal
// callback. cmd/loadtest/run.go uses that callback to build a TemplateContext
// from the final Snapshot, render the configured template into a plain-text
// body, optionally attach the per-scenario log buffer and --log-file, and
// hand the resulting message off to Sender.SendAsync.
//
// SendAsync returns immediately; a background goroutine performs the SMTP
// dialogue (STARTTLS + PLAIN/LOGIN on port 587 by default). The goroutine
// stores its progress in a *Status that the TUI polls so the Overview tab
// can show "sending", "sent", or "failed" without blocking the main loop.
//
// Configuration comes from three layers merged in this order (later wins):
//
//  1. The scenario's [email] block (per-test defaults)
//  2. --mail-config <file>           (shared cross-scenario settings)
//  3. Individual CLI flags           (one-off overrides)
//
// Each field is merged independently, so a user can put recipients and SMTP
// host in a shared file and override the subject on the command line for one
// run. See merge.go for the precedence rules.
//
// Tests use github.com/mhale/smtpd to run a real SMTP server in-process on
// a random port. The mock server captures envelopes and bodies so tests can
// assert on them without touching the network.
package mail
