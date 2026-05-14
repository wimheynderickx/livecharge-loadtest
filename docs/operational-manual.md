# Operational Manual — Livecharge OCS LoadTest

**Binary:** `loadtest`  
**Version:** see `loadtest version`

---

## Table of Contents

1. [Goal](#1-goal)
2. [Installation](#2-installation)
3. [Quick Start](#3-quick-start)
4. [Commands](#4-commands)
   - [run](#41-run)
   - [validate](#42-validate)
   - [mock](#43-mock)
   - [version](#44-version)
5. [Writing a Scenario File](#5-writing-a-scenario-file)
6. [Writing a Suite File](#6-writing-a-suite-file)
7. [Writing a Mock Config File](#7-writing-a-mock-config-file)
8. [TUI Dashboard — Controls](#8-tui-dashboard--controls)
9. [Running Without a Terminal (CI / Docker)](#9-running-without-a-terminal-ci--docker)
10. [Docker](#10-docker)
11. [CSV Reports](#11-csv-reports)
12. [Common Recipes](#12-common-recipes)
13. [Troubleshooting](#13-troubleshooting)

---

## 1. Goal

`loadtest` is a standalone Go CLI load-testing tool. It sends JSON payloads over NATS or HTTP(S), supports multi-step sessions with conditional branching, measures latency and throughput, and renders a live TUI dashboard. Configuration is entirely TOML-based. A built-in mock server enables self-contained testing without any dependencies.

This tool was developed initially by Wim Heynderickx, for testing the LiveCharge Online Charging System & Billing Suite.

---

## 2. Installation

### Build from source

Requirements: Go 1.22 or later.

```bash
git clone <repo-url> loadtest
cd loadtest
go build -o loadtest ./cmd/loadtest
```

Move the binary somewhere on your `$PATH`:

```bash
mv loadtest /usr/local/bin/
loadtest version
```

### Docker

```bash
docker build -t loadtest .
docker run loadtest version
```

---

## 3. Quick Start

**Step 1 — Start a mock server** (so you have something to test against):

```bash
loadtest mock scenarios/mock/nats-mock.toml
```

**Step 2 — Run a scenario** (in a second terminal):

```bash
loadtest run scenarios/nats-session.toml
```

The TUI dashboard opens. Press `q` to quit when done.

---

## 4. Commands

### 4.1 `run`

Runs one or more scenario files or a suite file. Launches the TUI dashboard.

```text
loadtest run [flags] <file> [file ...]
```

**Arguments:**

| Argument | Description |
| --- | --- |
| `<file>` | One or more `.toml` files. Pass a scenario file or a suite file. |

**Flags:**

| Flag | Default | Description |
| --- | --- | --- |
| `--config-dirs <dirs>` | `.` | Comma-separated directories to search for TOML files. Applies to files on the command line and `file =` references inside suite files. |
| `--no-tui` | false | Disable the TUI; print stats as plain text to stdout. Use in CI or Docker. |
| `--log-file <path>` | (none) | Write structured JSON logs (errors, state changes) to a file. |

**Examples:**

```bash
# Run a single scenario
loadtest run scenarios/charge-flow.toml

# Run two scenarios simultaneously
loadtest run scenarios/charge-flow.toml scenarios/auth-flow.toml

# Run a suite
loadtest run scenarios/suite-full-regression.toml

# Search for scenario files in multiple directories
loadtest run charge-flow.toml --config-dirs ./scenarios,~/shared-scenarios

# Headless mode (no TUI) with log file
loadtest run charge-flow.toml --no-tui --log-file ./run.log
```

---

### 4.2 `validate`

Parses and validates one or more TOML files without running anything. Exits with code 0 on
success, non-zero on error. Useful in CI to catch config mistakes early.

```text
loadtest validate [flags] <file> [file ...]
```

**Flags:**

| Flag | Default | Description |
| --- | --- | --- |
| `--config-dirs <dirs>` | `.` | Same as `run` — used to resolve file references in suite files. |

**Examples:**

```bash
# Validate a single scenario
loadtest validate scenarios/charge-flow.toml

# Validate all scenarios in a directory
loadtest validate scenarios/*.toml

# Validate a suite (also validates all referenced scenario files)
loadtest validate scenarios/suite-full-regression.toml
```

**Exit codes:**

| Code | Meaning |
| --- | --- |
| 0 | All files are valid |
| 1 | One or more files have errors (details printed to stderr) |

---

### 4.3 `mock`

Starts a lightweight mock NATS subscriber or HTTP server. Intended for testing the load tool
itself without needing a real OCS system. The mock stays running until you press Ctrl-C.

```text
loadtest mock [flags] <file>
```

**Arguments:**

| Argument | Description |
| --- | --- |
| `<file>` | A mock config TOML file (see [§7](#7-writing-a-mock-config-file)). |

**Flags:**

| Flag | Default | Description |
| --- | --- | --- |
| `--config-dirs <dirs>` | `.` | Search directories for the mock config file. |

**Examples:**

```bash
# Start a NATS mock
loadtest mock scenarios/mock/nats-mock.toml

# Start an HTTP mock
loadtest mock scenarios/mock/http-mock.toml
```

**Output:** The mock prints one line per request received:

```text
[mock] ocs.charge.initiate  → ok    chargeId=1042  (reply: 0.3ms)
[mock] ocs.charge.initiate  → fail  chargeId=1043  (reply: 0.1ms)
[mock] ocs.charge.confirm   → ok    (reply: 0.2ms)
```

---

### 4.4 `version`

Prints the binary version and exits.

```text
loadtest version
```

---

## 5. Writing a Scenario File

A scenario file is a TOML file that describes one load test. Every section is explained below.

### 5.1 `[scenario]` — Identity

```toml
[scenario]
name        = "charge-flow"           # short identifier, used in TUI and CSV
description = "Charging session test" # human-readable, shown in TUI header
```

### 5.2 `[transport]` — Connection

```toml
[transport]
type = "nats"                         # "nats" | "http" | "https"
url  = "nats://localhost:4222"        # connection URL

[transport.auth]
type = "none"                         # auth type (see below)
```

**Auth types:**

| Type | Required fields | Protocol |
| --- | --- | --- |
| `none` | — | NATS, HTTP, HTTPS |
| `userpass` | `username`, `password` | NATS |
| `basic` | `username`, `password` | HTTP, HTTPS |
| `jwt` | `token` | HTTP, HTTPS |

```toml
# NATS username/password
[transport.auth]
type     = "userpass"
username = "ocs_user"
password = "secret"

# HTTP Basic auth
[transport.auth]
type     = "basic"
username = "api_user"
password = "api_password"

# HTTP JWT Bearer
[transport.auth]
type  = "jwt"
token = "eyJhbGciOiJSUzI1NiIsI..."
```

For HTTP/HTTPS, `url` is the base URL. Each step's `path` is appended to it:

```toml
[transport]
type = "https"
url  = "https://ocs.internal:8443"
```

### 5.3 `[context]` — Variables

Context variables are available as `{{.ctx.name}}` in step templates. They are evaluated
once per session start.

```toml
[context]
# Static value — same for every session
terminalId = "TERM-001"

# Sequential counter — each new session gets the next number (thread-safe)
[context.chargeId]
type  = "sequence"
start = 1000       # first value
step  = 1          # increment per session

# Random integer in a range — different each session
[context.amount]
type = "random_range"
min  = 100
max  = 9999

# Random pick from a list — different each session
[context.msisdn]
type   = "random_pick"
values = ["31612345678", "31698765432", "31677777777"]
```

### 5.4 `[load]` — Load Parameters

```toml
[load]
rate             = 500      # target messages/second (0 = as fast as possible)
total_messages   = 100000   # stop after sending this many messages (0 = run forever)
duration         = "10m"    # stop after this long (0 = run forever)
concurrency      = 50       # number of parallel sessions running at the same time
response_timeout = "2s"     # max time to wait for a reply before recording a timeout error
```

If both `total_messages` and `duration` are set, the scenario stops when **either** limit is
reached first.

### 5.5 `[metrics]` — Statistics Configuration

```toml
[metrics]
percentiles = [50, 75, 90, 95, 99, 99.9]   # which latency percentiles to display and export
```

### 5.6 `[report]` — CSV Export (optional)

```toml
[report]
csv_path         = "./results/charge-flow-{timestamp}.csv"
timestamp_format = "2006-01-02T15-04-05"
overwrite        = true
flush_interval   = "10s"
```

Omit this section entirely if you do not need CSV output.

**`csv_path`** — the file to write. The special placeholder `{timestamp}` is replaced with
the scenario start time when the file is opened. This ensures each run produces a uniquely
named file.

**`timestamp_format`** — controls how `{timestamp}` is formatted. Uses Go's reference time
layout (the reference moment is `Mon Jan 2 15:04:05 MST 2006`). Common examples:

| Format string | Example output |
| --- | --- |
| `2006-01-02T15-04-05` (default) | `2026-05-13T21-04-17` |
| `2006-01-02` | `2026-05-13` |
| `20060102-150405` | `20260513-210417` |
| `2006-01-02T15:04:05Z07:00` | `2026-05-13T21:04:17+02:00` |

**`overwrite`** — controls what happens if the resolved file path already exists:

- `true` (default): the existing file is truncated and a fresh header row is written
- `false`: new rows are appended to the existing file (useful for resuming a long-running scenario)

### 5.7 `[[step]]` — Request Steps

A session executes steps in order. Each step sends one message and (unless `fire_and_forget`
is true) waits for a reply.

```toml
[[step]]
name    = "initiate-charge"
subject = "ocs.charge.initiate"    # NATS subject
template = """
{
  "terminalId": "{{.ctx.terminalId}}",
  "chargeId":   {{.ctx.chargeId}},
  "amount":     {{.ctx.amount}}
}
"""
```

**For HTTP steps,** use `method` and `path` instead of `subject`:

```toml
[[step]]
name     = "create-charge"
method   = "POST"
path     = "/v1/charges"
template = """{ "amount": {{.ctx.amount}} }"""
```

**Fire-and-forget (NATS only):** Send and move on without waiting for a reply.

```toml
[[step]]
name            = "emit-event"
subject         = "ocs.events.charge"
fire_and_forget = true
template        = """{ "event": "started", "id": {{.ctx.chargeId}} }"""
```

**Step-level timeout override:**

```toml
[[step]]
name             = "slow-enrichment"
subject          = "ocs.enrich"
response_timeout = "10s"             # overrides [load] response_timeout for this step only
template         = """{ "id": {{.ctx.chargeId}} }"""
```

**Custom headers** (works for both NATS and HTTP):

```toml
[[step.header]]
name  = "X-Correlation-Id"
value = "{{.ctx.chargeId}}"
```

### 5.8 `[[step.extract]]` — Extract Fields from Reply

After a reply arrives, you can extract JSON fields and store them as session variables.
Session variables are available as `{{.session.name}}` in subsequent step templates.

```toml
[[step.extract]]
field = "chargeRef"               # stored as {{.session.chargeRef}}
path  = "response/chargeRef"      # slash-separated JSON path into the reply body
```

**Path prefixes for different reply sources:**

| Path prefix | Source |
| --- | --- |
| `response/field` or `body/field` | JSON body field |
| `header/X-Header-Name` | HTTP response header |
| `status` | HTTP status code (as a string, e.g. `"200"`) |
| `meta/X-Header-Name` | NATS reply message header |

### 5.9 `[[step.predicate]]` — Conditional Flow

Predicates inspect extracted session values and decide what happens next. The first predicate
whose condition matches wins. If no predicate matches, execution continues to the next step.

```toml
[[step.predicate]]
name      = "happy-flow"           # name used in metrics and TUI
field     = "session.status"       # which field to test
op        = "eq"                   # eq | ne | contains | gt | lt
value     = "APPROVED"             # value to compare against
next_step = "confirm-charge"       # jump to this step name; "" = end session early
```

**Operators:**

| Op | Meaning |
| --- | --- |
| `eq` | equal |
| `ne` | not equal |
| `contains` | string contains substring |
| `gt` | greater than (numeric) |
| `lt` | less than (numeric) |

---

## 6. Writing a Suite File

A suite file runs multiple scenarios simultaneously.

```toml
[suite]
name = "Full Regression"

[[suite.scenario]]
file = "scenarios/charge-flow.toml"

[[suite.scenario]]
file = "scenarios/auth-flow.toml"

[[suite.scenario]]
file = "scenarios/refund-flow.toml"
```

File paths in `[[suite.scenario]]` are resolved relative to the suite file's own directory,
then relative to each `--config-dirs` directory in order.

---

## 7. Writing a Mock Config File

The mock server is a test double for OCS. It listens on NATS subjects or HTTP paths and
replies with a configurable success/failure response.

```toml
[mock]
type = "nats"                         # "nats" | "http"
url  = "nats://localhost:4222"

[mock.auth]
type = "none"

[[mock.endpoint]]
subject   = "ocs.charge.initiate"     # NATS subject to subscribe to
fail_rate = 0.05                      # fraction of requests that return fail_response (0.0–1.0)

  # Extract a field from the incoming request body
  [[mock.endpoint.extract]]
  field = "chargeId"                  # name available as {{.extracted.chargeId}} in templates
  path  = "chargeId"                  # JSON path in the request body

  # Reply sent when this request succeeds (1 - fail_rate of the time)
  [mock.endpoint.ok_response]
  template = """
  {
    "status":    "APPROVED",
    "chargeRef": "REF-{{.extracted.chargeId}}"
  }
  """

  # Reply sent when this request fails (fail_rate of the time)
  [mock.endpoint.fail_response]
  template = """
  {
    "status": "REJECTED",
    "reason": "INSUFFICIENT_BALANCE"
  }
  """
```

For **HTTP mock endpoints**, replace `subject` with `method` and `path`:

```toml
[[mock.endpoint]]
method    = "POST"
path      = "/v1/charges"
fail_rate = 0.02
```

---

## 8. TUI Dashboard — Controls

The TUI opens automatically when you run `loadtest run` without `--no-tui`.

### Layout

```text
┌─ header ─────────────────────────────────────────────────────────────────┐
│ ┌── sidebar ──────────┬── detail panel ─────────────────────────────────┐│
│ │ Scenario list       │ [1]Overview [2]Latency [3]Predicates [4]Log     ││
│ │ with state + actions│                                                  ││
│ │                     │  Content for selected scenario + tab             ││
│ │ ─────────────────   │                                                  ││
│ │ Suite totals        │                                                  ││
│ └─────────────────────┴──────────────────────────────────────────────────┘│
└───────────────────────────────────────────────────────────────────────────┘
```

### Keyboard Shortcuts

| Key | Action |
| --- | --- |
| `↑` | Move selection up in scenario sidebar |
| `↓` | Move selection down in scenario sidebar |
| `1` | Switch to Overview tab |
| `2` | Switch to Latency histogram tab |
| `3` | Switch to Predicates table tab |
| `4` | Switch to Log tab |
| `s` | Stop the selected scenario (keeps metrics; can be resumed) |
| `r` | Resume a stopped scenario (continues from where it left off) |
| `R` | Restart the selected scenario (resets all counters and metrics) |
| `Space` | Start a scenario that is in IDLE state |
| `q` | Quit — stops all running scenarios and exits |

### Scenario States

| Badge | Meaning |
| --- | --- |
| `RUNNING` (green) | Actively sending messages |
| `STOPPED` (yellow) | Paused; can be resumed or restarted |
| `IDLE` (grey) | Not yet started |
| `DONE` (blue) | Reached `total_messages` or `duration` limit |
| `ERROR` (red) | Stopped due to a fatal transport error |

---

## 9. Running Without a Terminal (CI / Docker)

Use `--no-tui` to disable the dashboard and print stats to stdout:

```bash
loadtest run charge-flow.toml --no-tui
```

Output format (one line per `flush_interval`):

```text
2026-05-13T21:04:17Z  charge-flow  RUNNING  sent=74231  rcvd=74228  err=3  msg/s=487  p50=12ms  p99=112ms
2026-05-13T21:04:27Z  charge-flow  RUNNING  sent=79011  rcvd=79008  err=3  msg/s=492  p50=11ms  p99=108ms
```

Combine with `--log-file` to capture errors separately:

```bash
loadtest run charge-flow.toml --no-tui --log-file ./run-errors.log
```

---

## 10. Docker

### Run a scenario

```bash
# Mount your scenarios directory as a volume
docker run \
  -v "$(pwd)/scenarios:/scenarios" \
  loadtest \
  run /scenarios/charge-flow.toml --no-tui
```

### Run load tool + mock server together

Create a `docker-compose.yml`:

```yaml
services:
  nats:
    image: nats:latest
    ports:
      - "4222:4222"

  mock:
    image: loadtest
    command: mock /scenarios/mock/nats-mock.toml
    volumes:
      - ./scenarios:/scenarios
    depends_on:
      - nats

  loadtest:
    image: loadtest
    command: run /scenarios/charge-flow.toml --no-tui --log-file /results/run.log
    volumes:
      - ./scenarios:/scenarios
      - ./results:/results
    depends_on:
      - mock
```

```bash
docker compose up
```

---

## 11. CSV Reports

Enable CSV output in your scenario's `[report]` block:

```toml
[report]
csv_path       = "./results/charge-flow.csv"
flush_interval = "10s"
```

A new row is appended every `flush_interval`. The file is not overwritten when the tool
restarts — rows are always appended. Move or rename the file between runs if you want
separate result sets.

**Column reference:**

| Column | Description |
| --- | --- |
| `timestamp` | ISO-8601 UTC timestamp of the snapshot |
| `scenario` | Scenario name |
| `messages_sent` | Cumulative messages sent |
| `messages_received` | Cumulative replies received |
| `errors` | Cumulative error count (timeouts + transport errors) |
| `msg_per_sec` | Current throughput (messages/second) |
| `p50_ms` | 50th percentile latency in milliseconds |
| `p95_ms` | 95th percentile latency in milliseconds |
| `p99_ms` | 99th percentile latency in milliseconds |
| `predicate_<name>_count` | Count of matches for each named predicate |

Percentile columns reflect the `[metrics] percentiles` list in the scenario file.

---

## 12. Common Recipes

### One-shot single message (smoke test)

```toml
[load]
rate           = 0
total_messages = 1
concurrency    = 1
```

### As-fast-as-possible stress test

```toml
[load]
rate           = 0       # 0 = no rate limit
total_messages = 0       # 0 = unlimited
duration       = "5m"
concurrency    = 100
```

### Sustained soak test (overnight)

```toml
[load]
rate           = 100
total_messages = 0
duration       = "12h"
concurrency    = 20

[report]
csv_path       = "./results/soak-test.csv"
flush_interval = "60s"
```

### Test happy vs error ratio

Define two predicates on a step:

```toml
[[step.predicate]]
name  = "happy-flow"
field = "session.status"
op    = "eq"
value = "APPROVED"

[[step.predicate]]
name  = "error-flow"
field = "session.status"
op    = "eq"
value = "REJECTED"
```

Watch the Predicates tab in the TUI — it shows count and percentage for each outcome in
real time.

---

## 13. Troubleshooting

### "connection refused" on NATS

- Check that the NATS server is running: `nats-server` or `docker run nats`
- Verify the `url` in `[transport]` matches the server address and port (default `4222`)
- If using `userpass` auth, verify credentials against the NATS server config

### "connection refused" on HTTP

- Verify `[transport] url` (base URL) and each step's `path`
- For HTTPS, ensure the server certificate is trusted (add CA to system trust store or use
  a reverse proxy that handles TLS termination)

### All replies time out

- Increase `response_timeout` in `[load]`
- Check that the target service is subscribed to the correct NATS subject or HTTP path
- Use `loadtest mock` to isolate whether the issue is in the load tool or the target

### TUI does not render correctly

- Ensure your terminal is at least 80 columns wide
- Use a terminal that supports 256 colours (e.g. iTerm2, Windows Terminal, most Linux terminals)
- In environments without a real TTY (CI, Docker), use `--no-tui`

### CSV file not created

- Check the directory in `csv_path` exists and is writable
- Confirm the `[report]` section is present in the scenario TOML (it is optional; omitting
  it disables CSV output)

### Sequence counter does not reset after Restart

- `Restart()` resets the sequence to its configured `start` value. If you see stale values,
  ensure you used `R` (uppercase restart) in the TUI, not `r` (resume).
