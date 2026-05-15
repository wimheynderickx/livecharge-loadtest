package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWarning_ExprFieldUnused builds a scenario that should trigger the
// op=expr+field warning and asserts the CLI prints it on stderr.
func TestWarning_ExprFieldUnused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "warn.toml")
	if err := os.WriteFile(path, []byte(`
[scenario]
name = "warn"
[transport]
type = "http"
url  = "http://localhost"
[transport.auth]
type = "none"
[load]
rate = 1
total_messages = 1
concurrency = 1
response_timeout = "1s"
[metrics]
percentiles = [50]
[[step]]
name = "s"
method = "GET"
path = "/"
template = "{}"
[[step.predicate]]
name  = "p"
op    = "expr"
value = "status == 200"
field = "status"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("./bin/loadtest", "validate", "--config", path)
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // warnings are non-fatal; exit code may be 0.
	out := stderr.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "field is unused") {
		t.Fatalf("expected warning on stderr; got: %q", out)
	}
}
