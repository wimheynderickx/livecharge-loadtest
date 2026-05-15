# Operational Manual — Livecharge OCS LoadTest

**Binary:** `loadtest`  
**Version:** see `loadtest version`

---

`loadtest` is a standalone Go CLI load-testing tool. It sends JSON payloads over NATS or HTTP(S), supports multi-step sessions with conditional branching, measures latency and throughput, and renders a live TUI dashboard. Configuration is entirely TOML-based. A built-in mock server enables self-contained testing without any dependencies.

## Main features

- **Live TUI dashboard** to dynamically interact with your testcases — start, stop, resume, restart, add and remove scenarios on the fly.
- **Multi-protocol transport** — NATS and HTTP/HTTPS with `none`, `userpass`, HTTP Basic, and JWT Bearer auth.
- **HTTP/2 client** — drive endpoints over h2c (`h2c://`) or h2-over-TLS (`https://` with ALPN). Force h1.1 over HTTPS via `[transport].http2 = false`.
- **Multi-step sessions** with JSON / header / status-code extraction and predicate-driven conditional flow.
- **Expression predicates** via `op = "expr"` — write boolean expressions over the response body, session vars, and scenario context using [expr-lang/expr](https://github.com/expr-lang/expr).
- **Sub-millisecond latency measurement** using HDR histograms with configurable buckets (auto or fully manual edges).
- **High Performance** as it can send thousands of messages per second.
- **Realtime throughput stats** — current, peak, and lifetime-average msg/sec.
- **CSV export** with float-ms latency columns, predicate counts, and `{timestamp}` placeholders.
- **Suite runs** — execute multiple scenarios concurrently from a single suite file.
- **Built-in mock server** (NATS + HTTP) with configurable `fail_rate` and `no_answer_rate` for realistic failure simulation.
- **Mock server HTTPS + HTTP/2** — `loadtest mock` serves TLS (auto-generated cert or user-supplied), HTTP/2 (h2c or h2 via ALPN), and per-endpoint chunked streaming responses.
- **Post-run email notifications** over SMTP (STARTTLS + auth) with text, HTML, or `multipart/alternative` body, log attachments, and templated subject/body.
- **Headless / CI mode** with structured logs and proper exit codes.
- **Embedded operational manual** — view it from inside the TUI (`m`) or via `loadtest manual`.

---

## Table of Contents

1. [Goal](#1-goal)
2. [Installation](#2-installation)
3. [Quick Start](#3-quick-start)
4. [Commands](#4-commands)
   - [run](#41-run)
   - [validate](#42-validate)
   - [mock](#43-mock)
   - [help](#44-help)
   - [manual](#45-manual)
   - [version](#46-version)
5. [Writing a Scenario File](#5-writing-a-scenario-file)
6. [Writing a Suite File](#6-writing-a-suite-file)
7. [Writing a Mock Config File](#7-writing-a-mock-config-file)
8. [Email Notifications](#8-email-notifications)
9. [TUI Dashboard — Controls](#9-tui-dashboard--controls)
10. [Running Without a Terminal (CI / Docker)](#10-running-without-a-terminal-ci--docker)
11. [Docker](#11-docker)
12. [CSV Reports](#12-csv-reports)
13. [Common Recipes](#13-common-recipes)
14. [Troubleshooting](#14-troubleshooting)

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
loadtest run [flags] [file ...]
```

**Arguments:**

| Argument | Description |
| --- | --- |
| `[file ...]` | Zero or more `.toml` scenario or suite files. These are **explicit** — they always auto-start, even when `--auto-start=false`. |

**Flags:**

| Flag | Default | Description |
| --- | --- | --- |
| `--config-dirs <dirs>` | (none) | Comma-separated directories to scan for TOML files. Found scenarios are **implicit** — they respect `--auto-start`. Also feeds the **Add scenario** picker (`a`) and the **file browser** (`b`). |
| `--auto-start` | `true` | When `true`, all scenarios (explicit and implicit) start immediately. When `false`, only explicitly given files auto-start; scenarios found via `--config-dirs` are loaded as **IDLE** and must be started manually with `Space`. |
| `--no-tui` | false | Disable the TUI; print stats as plain text to stdout. Use in CI or Docker. |
| `--log-file <path>` | (none) | Write structured JSON logs (errors, state changes) to a file. |
| `--config <file>` | (none) | Single scenario file (always auto-starts). Shorthand alternative to a positional argument. |
| `--suite <file>` | (none) | Suite file (always auto-starts all referenced scenarios). |

**Examples:**

```bash
# Run a single scenario (auto-starts)
loadtest run scenarios/charge-flow.toml

# Run two scenarios simultaneously (both auto-start)
loadtest run scenarios/charge-flow.toml scenarios/auth-flow.toml

# Load all scenarios from a directory; pick which ones to start manually
loadtest run --config-dirs ./scenarios --auto-start=false

# Load a directory but auto-start one specific scenario
loadtest run scenarios/charge-flow.toml --config-dirs ./scenarios --auto-start=false

# Run a suite
loadtest run scenarios/suite-full-regression.toml

# Headless mode (no TUI) with log file
loadtest run charge-flow.toml --no-tui --log-file ./run.log
```

### Auto-start behaviour in detail

| Source | `--auto-start=true` (default) | `--auto-start=false` |
| --- | --- | --- |
| Positional file args | starts immediately | starts immediately |
| `--config` / `--suite` | starts immediately | starts immediately |
| `--config-dirs` files | starts immediately | loaded as **IDLE** |

When `--auto-start=false` and `--config-dirs` is given, all discovered scenarios appear in
the TUI sidebar with an `IDLE` badge. Press `Space` to start the selected one, or use the
scenario control keys (`r`, `R`) as usual. This is useful when you want to inspect the
scenario list before committing to a run, or when you want to stagger the start of multiple
scenarios manually.

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
[mock] ocs.charge.initiate  → ok        chargeId=1042  (reply: 0.3ms)
[mock] ocs.charge.initiate  → fail      chargeId=1043  (reply: 0.1ms)
[mock] ocs.charge.initiate  → no-answer chargeId=1044
[mock] ocs.charge.confirm   → ok        (reply: 0.2ms)
```

---

### 4.4 `help`

Prints a curated quick-reference overview of all commands and common flags.

```text
loadtest help [command]
```

With no arguments: prints the brief overview. With a command name, delegates to `cobra` for
full flag documentation:

```bash
loadtest help
loadtest help run
loadtest help mock
```

---

### 4.5 `manual`

Displays the full operational manual (this document) inside the terminal.

```text
loadtest manual [flags]
```

**Flags:**

| Flag | Default | Description |
| --- | --- | --- |
| `--raw` | false | Print raw Markdown source instead of rendering it |
| `--no-pager` | false | Dump the rendered text directly to stdout (no interactive viewport) |

**Behaviour:**

- In an interactive terminal: opens a scrollable viewport. Use `↑` / `↓` or `j` / `k` to
  scroll, `PgUp` / `PgDn` for larger jumps, `g` / `G` to jump to the top or bottom, and
  `q` or `Esc` to exit.
- When piped (e.g. `loadtest manual | less`): prints the rendered text to stdout directly
  (same as `--no-pager`).

You can also read the manual from inside the TUI dashboard by pressing `m` (see §8).

---

### 4.6 `version`

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

### 5.2.1 Protocol selection

The client picks an HTTP protocol from the URL scheme:

| Scheme | Behaviour |
| --- | --- |
| `http://host:port/...` | HTTP/1.1 (default) |
| `h2c://host:port/...` | HTTP/2 over plain TCP, prior knowledge |
| `https://host:port/...` | TLS, ALPN negotiates `h2` or `http/1.1` (server picks) |

Force HTTP/1.1 over HTTPS with `http2 = false`:

```toml
[transport]
type  = "http"
url   = "https://api.example.com"
http2 = false   # advertise only http/1.1 in ALPN
```

#### TLS knobs

```toml
[transport.tls]
insecure_skip_verify = false   # default false; true for self-signed certs
ca_file              = ""      # extra PEM bundle on top of system roots
server_name          = ""      # SNI override; defaults to URL hostname
```

`insecure_skip_verify = true` emits one stderr warning at scenario startup (`WARN: TLS verification disabled ...`) so it never goes unnoticed in CI logs.

#### Conflicts validated at startup

- `h2c://` URL with `http2 = false` → rejected (the flag has no effect).
- `[transport.tls]` on a non-`https://` URL → rejected (silently meaningless).
- `ca_file` pointing at a non-existent path → rejected with the file error surfaced verbatim.

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
percentiles      = [50, 75, 90, 95, 99, 99.9]   # which latency percentiles to display and export

# Histogram bucket layout — choose one of the two options below:

# Option A: automatic layout with a fixed number of buckets (default: 20)
bucket_count     = 30

# Option B: fully manual bucket edges in milliseconds (supports sub-millisecond values)
bucket_edges_ms  = [0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 50, 100, 500, 1000]
```

**`bucket_count`** — The tool automatically places `bucket_count` evenly-spaced buckets
across the full latency range. 75% of buckets cover the first 50% of the range (finer
resolution for the common low-latency case), and the remaining 25% cover the tail. Default
value is 20 when neither option is specified.

**`bucket_edges_ms`** — When you know exactly which latency thresholds matter, list them
explicitly in milliseconds. Sub-millisecond values such as `0.01` (10 µs) and `0.25`
(250 µs) are supported because latency is measured at **microsecond** resolution internally.

If both options are present, `bucket_edges_ms` wins.

**Latency display:** All latency values (percentiles, histogram bucket labels, CSV columns)
use adaptive decimal formatting:

| Value | Display |
| --- | --- |
| 135 µs | `0.135 ms` |
| 1.23 ms | `1.23 ms` |
| 12.0 ms | `12 ms` |

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

**Multi-step sessions** chain multiple steps together. Session variables extracted from one
step's reply are available in subsequent steps via `{{.session.name}}`:

```toml
[[step]]
name   = "create-charge"
method = "POST"
path   = "/v1/charges"
template = """{ "amount": {{.ctx.amount}} }"""

  [[step.extract]]
  field = "chargeRef"
  path  = "response/chargeRef"

[[step]]
name   = "query-status"
method = "GET"
path   = "/v1/charges/{{.session.chargeRef}}"
template = ""

[[step]]
name   = "confirm-charge"
method = "POST"
path   = "/v1/charges/{{.session.chargeRef}}/confirm"
template = """{ "chargeRef": "{{.session.chargeRef}}" }"""
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
| `expr` | An [expr-lang/expr](https://github.com/expr-lang/expr) expression evaluating to a boolean. Reads `status`, `headers`, `body`, `session`, and `ctx` namespaces directly. See the "Expression predicates" subsection below. |

#### Expression predicates (`op = "expr"`)

Set `op = "expr"` to write a boolean expression that has access to the
full response. Example:

```toml
[[step.predicate]]
name      = "amount-matches"
op        = "expr"
value     = 'body.amount == ctx.amount && status == 200'
next_step = "confirm"
```

##### Quoting your expression in TOML

Expressions almost always contain double-quoted string literals
(`"OK"`). Use **TOML literal strings** (single quotes) for the `value`
so internal `"` doesn't need escaping:

```toml
# Recommended.
value = 'body.charges[0].status == "OK"'

# Multi-line literal — for compound expressions.
value = '''
  status == 200
  && body.code == "CREATED"
  && session.chargeId != ""
'''

# Basic string — every internal " needs escaping.
value = "body.charges[0].status == \"OK\""
```

Within the expression itself, expr-lang accepts double-quoted (`"..."`)
and backtick raw (`` `...` ``) string literals. Single quotes inside
the expression are not valid string delimiters in expr.

##### Available namespaces

| Namespace | Source | Type | Example |
| --- | --- | --- | --- |
| `status` | HTTP response status code | int | `status == 200` |
| `headers` | response headers (last value per name) | `map[string]string` | `headers["Content-Type"] contains "json"` |
| `body` | response body parsed as JSON (tree); raw string if not JSON | tree / string | `body.charges[0].status == "OK"` |
| `session` | values from previous `[[step.extract]]` | `map[string]string` | `session.chargeId != ""` |
| `ctx` | scenario `[context]` values | `map[string]string` | `body.amount == ctx.amount` |

`[[step.extract]]` is now **optional** — within the current step's
predicates you can read the response body directly via `body.*`.
Extract only when a *later* step needs the value, or when you want a
per-name latency histogram keyed on the extracted field.

##### Permissive missing-variable resolution

A reference to a missing path (e.g. `body.charges[3].id` when the array
has length 2) resolves to the **zero value of the inferred type**:
empty string for string operations, 0 for numeric, false for boolean.
You don't need to guard expressions with existence checks.

##### Runtime errors

If an expression hits a runtime error (e.g. comparing a JSON object to
an int directly), the predicate **does not match** (no `next_step`
taken). The error is logged once per unique `(predicate, error)` pair
per scenario lifetime — repeats are silenced. Scenarios never crash on
expression errors.

##### Built-in functions

Full expr-lang defaults are available: arithmetic, comparison, logical,
`in`, `contains`, `startsWith`, `endsWith`, `matches` (regex), `len`,
string methods (`upper`, `lower`, `trim`, `split`, `replace`, ...),
array predicates (`all`, `any`, `none`, `one`, `filter`, `map`,
`count`, `sum`, `mean`, `median`, `sort`, `take`), numeric (`abs`,
`ceil`, `floor`, `round`, `min`, `max`), date/time (`now`, `duration`).

Loadtest-specific additions:

| Function | Signature | Purpose |
| --- | --- | --- |
| `toInt(x)` | `any → int` | Explicit conversion. Returns 0 on failure (consistent with permissive rule). |
| `toFloat(x)` | `any → float64` | Same for float. |
| `toString(x)` | `any → string` | Same for string. |
| `env(name)` | `string → string` | Reads an environment variable. Returns `""` when unset. |

##### Compile errors

Expressions are compiled **once at scenario start**. A syntactically
invalid expression puts the runner in the `SCRIPT_ERROR` state; the
TUI sidebar shows `SCRIPT ERR` and the Overview tab shows the compiler
message verbatim. Healthy sibling scenarios in the same run are
unaffected.

##### Performance — when to prefer classic ops

`op = "expr"` is roughly **6× slower per evaluation** than the classic
ops (`eq | ne | contains | gt | lt`). Concretely on an Apple M-series
laptop: `expr` evaluates around 670 ns/op vs. ~110 ns/op for `eq`,
with extra map allocations per call (the permissive resolver clones
the namespace maps). Compilation itself is ~10 µs/predicate at scenario
start — paid once, never on the hot path.

In practice the **network round-trip dominates** every scenario: a
1 ms response makes the predicate cost noise. So for HTTP and NATS
load tests the difference is invisible at the throughput level.

When per-evaluation cost actually matters — typically synthetic
local-only scenarios with sub-millisecond response times, or very
high-rate runs (>100k req/s per worker) — **prefer the classic ops**.
They stay in the language and are not deprecated. A common pattern is:

```toml
# Hot-path predicate — use classic eq for speed.
[[step.predicate]]
name      = "ok"
op        = "eq"
field     = "status"
value     = "200"
next_step = "next"

# Cold-path or richer logic — expr is fine here.
[[step.predicate]]
name      = "amount-mismatch"
op        = "expr"
value     = 'body.amount != ctx.amount || status >= 400'
next_step = ""
```

Mix freely — the dispatcher picks the right evaluator per predicate.

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
subject        = "ocs.charge.initiate"   # NATS subject to subscribe to
fail_rate      = 0.05                    # fraction returning fail_response (0.0–1.0)
no_answer_rate = 0.02                    # fraction that send no reply at all (0.0–1.0)

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

**`fail_rate`** — Fraction of requests (0.0–1.0) for which the mock returns `fail_response`
instead of `ok_response`. For example, `0.05` means 5% of requests return a failure reply.
The loadtest tool counts these as successful responses (the reply was received), but your
predicate logic can detect the failure field.

**`no_answer_rate`** — Fraction of requests (0.0–1.0) for which the mock sends **no reply
at all**. This simulates a silent timeout — the OCS received the request but never replied.
The loadtest tool records these as timeout errors once `response_timeout` is reached.

- For **NATS**: the mock simply does not call `Respond`, so the caller's subscription
  receives nothing.
- For **HTTP**: the mock blocks until the client disconnects (i.e. until `response_timeout`
  expires on the client side).

Combined example: with `fail_rate = 0.05` and `no_answer_rate = 0.02`, roughly 5% of
requests get a failure reply, 2% get no reply (timeout), and 93% get a success reply.

For **HTTP mock endpoints**, replace `subject` with `method` and `path`:

```toml
[[mock.endpoint]]
method         = "POST"
path           = "/v1/charges"
fail_rate      = 0.02
no_answer_rate = 0.01
```

### 7.1 Mock TLS

A mock HTTP listener can serve HTTPS:

```toml
[transport]
type = "http"
url  = "localhost:8443"

[transport.tls]
enabled = true
# Optional override — when both are set, the mock loads the pair from disk.
# When both are empty, the mock generates a self-signed cert in memory.
# cert_file = "/path/to/cert.pem"
# key_file  = "/path/to/key.pem"
```

**Auto-generated cert behaviour:**

- ECDSA P-256 key.
- CN = `loadtest-mock`. SAN list: `localhost`, `127.0.0.1`, `::1`, `loadtest-mock`.
- Valid from boot − 1 minute to boot + 24 hours.
- Regenerated on every mock restart (no on-disk persistence in 0.2).

**Startup-log forms:**

```text
mock: listening on https://127.0.0.1:8443  (TLS, auto-generated self-signed cert, valid 24h)
mock: listening on https://127.0.0.1:8443  (TLS, cert=/path/to/cert.pem)
```

Clients connecting to the auto-generated cert must set
`[transport.tls].insecure_skip_verify = true` on the scenario side.

### 7.2 Mock HTTP/2 listener

The presence of `[transport.http2]` (even when empty) turns HTTP/2 on for
a mock listener. Combined with `[transport.tls].enabled = true`, ALPN
negotiates `h2` or `http/1.1`; without TLS, the mock speaks h2c
(cleartext HTTP/2 via prior knowledge).

| TLS | `[transport.http2]` | Result                                       |
| --- | ------------------- | -------------------------------------------- |
| off | absent              | HTTP/1.1 plain TCP (default)                 |
| off | present             | h2c — HTTP/2 cleartext via prior knowledge   |
| on  | absent              | HTTPS, ALPN offers `http/1.1` only           |
| on  | present             | HTTPS, ALPN offers both `h2` and `http/1.1`  |

**Tunables (all optional, 0 = library default):**

| Field                         | Range            | Maps to                                       |
| ----------------------------- | ---------------- | --------------------------------------------- |
| `max_concurrent_streams`      | 1 – 2³¹−1        | `http2.Server.MaxConcurrentStreams`           |
| `initial_stream_window_size`  | 1 – 2³¹−1        | `http2.Server.MaxUploadBufferPerStream`       |
| `initial_conn_window_size`    | ≥ stream window  | `http2.Server.MaxUploadBufferPerConnection`   |
| `max_frame_size`              | 16384 – 16777215 | `http2.Server.MaxReadFrameSize`               |

Worked examples: `mock/h2c-mock.toml` and `mock/h2-mock.toml`.

### 7.3 Streaming endpoints

Add `[endpoint.stream]` to a `[[endpoint]]` block to emit the OK response
in chunks with a delay between each:

```toml
[[endpoint]]
path        = "/v1/stream"
method      = "GET"
ok_response = "AAABBBCCC"

  [endpoint.stream]
  chunks   = 3        # split body into N roughly-equal slices
  delay_ms = 25       # delay between chunks
```

- Works for both HTTP/1.1 (chunked transfer encoding) and HTTP/2
  (multiple DATA frames).
- `fail_response` is **always** sent as a single response — streaming
  applies only to the OK branch.
- `chunks = 1` is a valid toggle-off (single chunk, no delay).
- `no_answer_rate` interacts cleanly: when the mock decides "no answer"
  for a request, no chunks are sent.

---

## 8. Email Notifications

`loadtest` can email a run-summary when a scenario finishes (DONE) or fails
fatally (ERROR). The feature is opt-in: with no `[email]` block and no `--mail-*`
flags, nothing is sent.

### 8.1 Configuration sources

Three layers compose into the final email configuration. Each is merged **per
field** — later layers override earlier ones, fields not touched fall through.

| Layer | Source | Precedence |
| --- | --- | --- |
| 1 | Scenario file `[email]` block | floor — per-scenario defaults |
| 2 | `--mail-config <file>` shared TOML | overrides layer 1 |
| 3 | Individual `--mail-*` CLI flags | overrides everything |

Set credentials in a shared `mail-config.toml`, the recipient list in the
scenario, and override the subject on the command line for a one-off run.
Each layer is optional; you can configure everything in any one of them.

**Exception — `report_interval`:** progress-mail cadence is inherently
scenario-specific (a soak test wants minutes between progress mails, a
smoke test wants seconds), so the scenario's `report_interval` wins over
the shared `mail-config.toml`. The file value only fills in when the
scenario didn't set one. CLI still wins over both, as for every other
field. This is the only field with inverted precedence.

The SMTP password also reads from the `LOADTEST_SMTP_PASS` environment
variable when no `smtp_pass` or `--mail-smtp-pass` is set — handy for CI.

### 8.2 Scenario `[email]` block

```toml
[email]
enabled         = true
on              = ["start", "progress", "done", "error"]   # see §8.2.1 below
send_timeout    = "30s"
report_interval = "5m"                 # cadence for "progress" emails (required when "progress" is in `on`)
attach_log      = true                 # attach scenario log + --log-file

smtp_host     = "smtp.gmail.com"
smtp_port     = 587                    # default 587 (STARTTLS submission)
smtp_user     = "user@example.com"
smtp_pass     = "app-password"

from          = "Livecharge OCS LoadTest <noreply@example.com>"
to            = ["alice@example.com", "bob@example.com"]
cc            = []
bcc           = []
subject       = "[Livecharge] {{.Scenario.Name}} — {{.State}}"

# Body templates — set zero, one, or both. The text and html slots are
# independent: when both are populated, the email goes out as
# multipart/alternative so every mail client picks the format it can
# render best. Empty slots fall back to the built-in plain-text default
# for text; HTML stays off unless you opt in.
template           = ""                                       # inline plain-text
template_file      = "mail-templates/mail-default.txt.tmpl"   # plain-text from file
template_html      = ""                                       # inline html
template_file_html = "mail-templates/mail-default.html.tmpl"  # html from file
```

**`enabled`** turns the feature on. **`attach_log`** attaches the
scenario's recent log buffer inline **and** the full `--log-file`
contents when set (capped at 1 MB — truncated tail with a marker line
when larger).

#### 8.2.1 Lifecycle triggers — what `on` accepts

`on` is a list naming which lifecycle events should send an email. The
default (when omitted) is `["done", "error"]` — terminal-only — so
existing configs keep their behaviour. Any combination is allowed:

| Trigger | Fires when… | Notes |
| --- | --- | --- |
| `start` | The scenario transitions IDLE → RUNNING from a fresh `Start()` or `Restart()`. | **Not** fired on `Resume()` (the scenario continues a prior run). The email reports `state = RUNNING`, sent and received counters at 0, no latency yet. |
| `progress` | First fire at `min(report_interval, 5s)` after start so short scenarios get at least one progress email; subsequent fires every `report_interval` while the scenario is RUNNING. | Requires `report_interval > 0`. The ticker pauses during STOPPED state and resumes on Resume. **Cancelled the moment the scenario reaches a terminal state** — a run that finishes between ticks gets no stale progress email after its done/error mail. |
| `done` | The scenario reaches its `total_messages` / `duration` limit **with mostly-successful results**. | Fired once. |
| `error` | The scenario reaches its limit but errors dominate (errors > received). | Fired once. Mutually exclusive with `done` for a given run — the email feature classifies the final outcome based on the snapshot. |

**`report_interval`** is required when `progress` is in `on`; the
loaded config is rejected at startup otherwise. A 5-minute cadence is
reasonable for soak / overnight tests; sub-minute intervals are
allowed but rarely useful (the TUI Overview tab already shows live
stats). Manual `Stop` (`s` in the TUI) does **not** fire any email —
stopping is user-initiated and the user is already at the terminal.

Templates can branch on the reason via `{{.Trigger}}`, e.g.:

```text
{{if eq .Trigger "error"}}🚨 OCS load test failed{{else}}{{.Scenario.Name}}{{end}}
```

The built-in HTML template renders a coloured banner per trigger; the
built-in text template prints a `=== Sent because: <trigger> ===`
header so the recipient never has to guess which event the mail is
about.

#### 8.2.2 Overview-tab indicator

When the email feature is on, the Overview tab shows a two-line summary
under the latency block so you can see *what's wired up* without
opening the scenario file:

```text
EMAIL  on: start, progress (every 10s), done, error
         📧 sent (progress) to ops@example.com at 14:22:35
```

The first line lists the active triggers and the resolved progress
cadence (handy when `--mail-config` overrides — or, for `report_interval`
specifically, *doesn't* override — the scenario value). The
most-recently-fired trigger is highlighted so the status line below
always reads in context. The status line appears only after the first
send fires.

### 8.3 `mail-config.toml` (shared file)

Same body as the `[email]` block but without the header — the whole file
*is* the email config:

```toml
enabled    = true
smtp_host  = "smtp.gmail.com"
smtp_port  = 587
smtp_user  = "user@example.com"
smtp_pass  = "app-password"
from       = "Livecharge OCS LoadTest <noreply@example.com>"
to         = ["ops@example.com"]
attach_log = true
```

Reference it with `--mail-config path/to/mail-config.toml`.

### 8.4 CLI flags

| Flag | Description |
| --- | --- |
| `--mail-config <file>` | Load shared email settings from this TOML file. |
| `--mail-enabled` / `--no-mail` | Force enable / disable regardless of TOML. `--no-mail` wins when both are passed. |
| `--mail-to addr,addr` | Recipients (To:). Comma-separated. |
| `--mail-cc addr,addr` | Recipients (Cc:). |
| `--mail-bcc addr,addr` | Recipients (Bcc:). Not exposed in headers; still RCPT'd. |
| `--mail-from addr` | From: address. Default: `Livecharge OCS LoadTest <noreply@livecharge.local>`. |
| `--mail-subject <tpl>` | Subject template (text/template). Default: `[Livecharge] {{.Scenario.Name}} — {{.State}}`. |
| `--mail-smtp-host host` | SMTP server hostname. |
| `--mail-smtp-port port` | SMTP server port. Default 587. |
| `--mail-smtp-user user` | SMTP auth username. |
| `--mail-smtp-pass pass` | SMTP auth password (or set `$LOADTEST_SMTP_PASS`). |
| `--mail-template <file>` | Path to body template file. Default: built-in. |
| `--mail-attach-log` | Attach scenario log + `--log-file` to the email. |
| `--mail-on done,error` | Lifecycle events that trigger the email. |

### 8.5 Body & subject templates

Subject and body use Go's `text/template` syntax. The same context is
exposed to both, so any field below is usable in either.

| Placeholder | Type | Example |
| --- | --- | --- |
| `{{.Scenario.Name}}` | string | `charge-flow` |
| `{{.Scenario.Description}}` | string | `Happy/error mix` |
| `{{.Scenario.Path}}` | string | `/scenarios/charge.toml` |
| `{{.State}}` | string | `DONE` / `ERROR` |
| `{{.Trigger}}` | string | `done` / `error` |
| `{{.StartedAt}}` | time.Time | use `.Format` |
| `{{.FinishedAt}}` | time.Time | |
| `{{.Elapsed}}` | string | `00:01:30` |
| `{{.Sent}}` `{{.Received}}` `{{.Errors}}` | int64 | tab-1 counters |
| `{{.MsgPerSec}}` `{{.MaxMsgPerSec}}` `{{.AvgMsgPerSec}}` | float64 | tab-1 throughput |
| `{{.Latency.p50}}` `{{.Latency.p99}}` `{{.Latency.p99.9}}` | string | tab-1 percentiles (already formatted, e.g. `0.135 ms`) |
| `{{range .Histogram}}` | iter `{Label, Count, Pct}` | tab-2 histogram |
| `{{range .Predicates}}` | iter `{Name, Count, Pct, P50, P95, P99}` | tab-3 predicates |

A bad placeholder in the subject template falls back to a literal subject
(`[Livecharge] <name> finished (<state>)`) so a typo doesn't lose the email.
A bad placeholder in the body template marks the email **Failed** and is
shown in the TUI Overview tab — partial bodies are worse than no email.

#### Plain-text, HTML, or both

The text and html slots are independent — set zero, one, or both:

| Configuration | What gets sent |
| --- | --- |
| neither set | Built-in plain-text default, MIME type `text/plain` |
| `template` or `template_file` only | Your text body, MIME type `text/plain` |
| `template_html` or `template_file_html` only | Your HTML body **plus** the built-in text default as a fallback, sent as `multipart/alternative` |
| both text *and* html templates set | Your text + your HTML, sent as `multipart/alternative` |

When `multipart/alternative` is used, every modern client (Gmail, Outlook,
Apple Mail, …) displays the HTML version; plain-text readers and
accessibility tools show the text version.

A starter HTML template ships in `mail-templates/mail-default.html.tmpl`.
Point at it like so:

```toml
[email]
template_file_html = "mail-templates/mail-default.html.tmpl"
# leave template / template_file unset to use the built-in text fallback
```

The built-in default text template covers every section the TUI shows
(overview, latency table, histogram, predicates).

### 8.6 Status display in the TUI

The Overview tab shows the email state at the bottom while it sends:

```text
EMAIL  📧 sending to alice@example.com…
EMAIL  📧 sent to alice@example.com at 15:42:18    (green)
EMAIL  📧 FAILED — auth: 535 Authentication failed (red)
```

When email is disabled for a scenario, the row is hidden entirely.

### 8.7 Async send and graceful shutdown

Email goes out in a background goroutine so a slow SMTP server can't
block scenario teardown. On `q` (quit), loadtest waits up to 60 s for any
in-flight sends; if a goroutine is still running at that point it's
abandoned and the scenario log carries a `FAILED — context deadline`
note.

### 8.8 Testing with a mock SMTP server

The test suite uses `github.com/mhale/smtpd` to spin up a real SMTP
server in-process on a random local port. Tests start one with
`newMockServer(t, authRequired bool)`, point the sender at its address,
and assert against captured envelopes:

```go
srv := newMockServer(t, /*authRequired=*/ false)
sender := srv.senderFor(Config{From: "alice@x", To: []string{"bob@x"}})
<-sender.SendAsync(Message{Body: "hi"}, &Status{})
if got := srv.Deliveries(); len(got) != 1 { ... }
```

This setup covers PLAIN/LOGIN auth, attachments, multipart MIME,
dial-failure recording, and BCC handling. See
`internal/mail/sender_test.go` for the full suite.

For ad-hoc end-to-end testing against a real-looking mailbox without a
real provider, run [MailHog](https://github.com/mailhog/MailHog) in
Docker on port 1025 and point `--mail-smtp-host=localhost
--mail-smtp-port=1025` at it.

---

## 9. TUI Dashboard — Controls

The TUI opens automatically when you run `loadtest run` without `--no-tui`.

### Layout

```text
┌─ Livecharge OCS  LoadTest ─────────────────────────────────────────────────┐
│ ┌── sidebar ──────────┬── detail panel ─────────────────────────────────┐  │
│ │ Scenario list       │ [1]Overview [2]Latency [3]Predicates [4]Log     │  │
│ │ with state + stats  │                                                  │  │
│ │                     │  Content for selected scenario + tab             │  │
│ │ ─────────────────   │                                                  │  │
│ │ Suite totals        │                                                  │  │
│ └─────────────────────┴──────────────────────────────────────────────────┘  │
└─ [s]top [r]esume [R]estart [a]dd [x]remove [m]anual [q]uit ────────────────┘
```

The **header bar** shows "Livecharge OCS" in light blue and "LoadTest" in white on a navy
background. The **footer bar** shows the active keyboard shortcuts in the same colour scheme.

### Overview Tab

The Overview tab shows two rows of KPIs for the selected scenario:

**Row 1 — Counters:**

```text
sent: 74,231   rcvd: 74,228   errors: 3   elapsed: 00:02:14
```

**Row 2 — Throughput (messages/second):**

```text
current: 487   MAX: 512   AVG: 483
```

- **current** — rolling 1-second window (refreshes every 100 ms)
- **MAX** — highest instantaneous rate ever recorded during this run
- **AVG** — lifetime average since the scenario started

### PROTOCOL field

The Overview tab shows the transport's wire-protocol label under the scenario description. It updates from "intent" to the negotiated value once the first response arrives:

| State | HTTP form | NATS form |
| --- | --- | --- |
| Pre-first-response | `HTTP/2 (h2c, intent)` / `HTTPS (h2 preferred, negotiating)` / `HTTPS (h1.1 forced)` | `NATS (connecting)` |
| Post-first-response | `HTTP/2 (h2)` / `HTTP/2 (h2c)` / `HTTP/1.1 (negotiated to h1.1)` / `HTTP/1.1` | `NATS 2.10` (version from server INFO) |

The "negotiated to h1.1" form makes ALPN misconfiguration visible at a glance — if you wired up `https://` expecting HTTP/2 but the server only offered h1.1 in its ALPN list, the field shows that immediately.

### Sidebar Totals

When multiple scenarios are running, the bottom of the sidebar shows aggregate totals across
all scenarios:

```text
─── totals ───────────────────
sent:    1,234,567
rcvd:    1,234,555
errors:  12
max/s:   2,143
avg/s:   1,987
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
| `a` | Open **Add scenario** picker — lists all `.toml` files found in `--config-dirs` |
| `x` | **Remove** the selected scenario (with confirmation prompt) |
| `b` | Open **file browser** — navigate the filesystem to pick any `.toml` file |
| `m` | Open the **manual** in a scrollable in-dashboard viewport |
| `q` | Quit — stops all running scenarios and exits |

### Add Scenario Picker (`a`)

Pressing `a` opens a modal that lists every `.toml` scenario file discovered in the
auto-scanned directories (see below). Scenarios already loaded are excluded. Use `↑` / `↓`
to navigate, `Enter` to load the highlighted scenario, `Esc` to cancel.

From inside the picker, press **`b`** to switch to the file browser — useful when the
scenario you want lives outside the scanned folders. The picker closes and the browser
opens at the current working directory; pick any `.toml` and you're back in the dashboard
with the new scenario added.

#### Which folders does the picker scan?

When `--config-dirs` is set at startup, those directories — and only those — are scanned.

When `--config-dirs` is **not** given, the picker auto-scans the current working
directory plus a short list of conventional subfolders that exist:

| Default scan locations (when no `--config-dirs`) |
| --- |
| `.` (the directory you launched `loadtest` from) |
| `./scenarios` / `./scenario` |
| `./templates` |
| `./tests` / `./test` |

Each subfolder is added only when it actually exists. To override entirely, pass
`--config-dirs path1,path2`. To reach anywhere else without reconfiguring, use the `b`
file browser from inside the picker.

### Remove Scenario (`x`)

Pressing `x` asks for confirmation before removing the currently selected scenario. Running
scenarios are stopped first, then their resources (transport connection, CSV writer) are
cleaned up. Press `y` / `Enter` to confirm or `n` / `Esc` to cancel.

### File Browser (`b`)

Pressing `b` opens a filesystem navigator. Only `.toml` files are selectable. Navigate with
`↑` / `↓`, open directories with `→` or `Enter`, go up a level with `←`. Press `Enter` on
a `.toml` file to load it as a new scenario.

`b` is also reachable from inside the `a` picker — handy when the scenario you want
isn't in the auto-scanned set.

### In-Dashboard Manual (`m`)

Pressing `m` opens the full operational manual rendered in a scrollable viewport inside the
TUI. Scroll with `↑` / `↓`, `j` / `k`, `PgUp` / `PgDn`, or `g` / `G`. Press `Esc` to
return to the normal dashboard view. The scroll percentage is shown in the footer.

### Scenario States

| Badge | Meaning |
| --- | --- |
| `RUNNING` (green) | Actively sending messages |
| `STOPPED` (yellow) | Paused; can be resumed or restarted |
| `IDLE` (grey) | Not yet started |
| `DONE` (blue) | Reached `total_messages` or `duration` limit |
| `ERROR` (red) | Stopped due to a fatal transport error |

---

## 10. Running Without a Terminal (CI / Docker)

Use `--no-tui` to disable the dashboard and print stats to stdout:

```bash
loadtest run charge-flow.toml --no-tui
```

Output format (one line per `flush_interval`):

```text
2026-05-13T21:04:17Z  charge-flow  RUNNING  sent=74231  rcvd=74228  err=3  msg/s=487  p50=0.135ms  p99=1.23ms
2026-05-13T21:04:27Z  charge-flow  RUNNING  sent=79011  rcvd=79008  err=3  msg/s=492  p50=0.140ms  p99=1.18ms
```

Combine with `--log-file` to capture errors separately:

```bash
loadtest run charge-flow.toml --no-tui --log-file ./run-errors.log
```

---

## 11. Docker

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

## 12. CSV Reports

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
| `p50_ms` | 50th percentile latency in milliseconds (float, e.g. `0.135`) |
| `p95_ms` | 95th percentile latency in milliseconds (float, e.g. `1.230`) |
| `p99_ms` | 99th percentile latency in milliseconds (float, e.g. `4.500`) |
| `predicate_<name>_count` | Count of matches for each named predicate |

Percentile columns reflect the `[metrics] percentiles` list in the scenario file. Values
are **float milliseconds** with three decimal places, giving sub-millisecond resolution
(e.g. `0.135` for 135 µs). This matches the adaptive decimal display in the TUI.

---

## 13. Common Recipes

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

### Fine-grained latency histogram

Use manual bucket edges to focus resolution in the range you care about:

```toml
[metrics]
percentiles     = [50, 95, 99, 99.9, 99.99, 100]
bucket_edges_ms = [0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 3, 4, 5, 10, 20, 50, 100, 200, 500, 1000]
```

### Three-step session (create → query → confirm)

```toml
[[step]]
name     = "create-charge"
method   = "POST"
path     = "/v1/charges"
template = """{ "amount": {{.ctx.amount}} }"""

  [[step.extract]]
  field = "chargeRef"
  path  = "response/chargeRef"

[[step]]
name     = "query-status"
method   = "GET"
path     = "/v1/charges/{{.session.chargeRef}}"
template = ""

[[step]]
name     = "confirm-charge"
method   = "POST"
path     = "/v1/charges/{{.session.chargeRef}}/confirm"
template = """{ "chargeRef": "{{.session.chargeRef}}" }"""
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

### Simulate timeouts in the mock

Use `no_answer_rate` to make the mock occasionally return nothing:

```toml
[[mock.endpoint]]
method         = "POST"
path           = "/v1/charges"
fail_rate      = 0.02      # 2% explicit failures
no_answer_rate = 0.01      # 1% silent timeouts
```

Set `response_timeout` in the scenario to control how long the client waits:

```toml
[load]
response_timeout = "2s"
```

### Email a run summary

Minimal scenario that emails its results on completion:

```toml
[email]
enabled    = true
smtp_host  = "smtp.example.com"
smtp_user  = "alerts@example.com"
smtp_pass  = ""                 # set LOADTEST_SMTP_PASS instead
from       = "Livecharge OCS LoadTest <noreply@example.com>"
to         = ["ops@example.com"]
attach_log = true
```

Run with credentials from the environment:

```bash
LOADTEST_SMTP_PASS=app-password loadtest run charge-flow.toml
```

Override the subject for one specific run without touching the scenario:

```bash
loadtest run charge-flow.toml \
  --mail-subject "[CI] {{.Scenario.Name}} {{.State}} in {{.Elapsed}}"
```

Force-disable email for a run that has it configured in TOML:

```bash
loadtest run charge-flow.toml --no-mail
```

---

## 14. Troubleshooting

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

### High timeout rate when using `no_answer_rate`

- This is expected behaviour — `no_answer_rate` deliberately causes the loadtest tool to
  wait for `response_timeout` and then record a timeout error
- Use the Predicates tab or the errors counter in the Overview tab to track the rate
- If the timeout rate is higher than `no_answer_rate`, check for real connectivity issues

### TUI does not render correctly

- Ensure your terminal is at least 80 columns wide
- Use a terminal that supports 256 colours (e.g. iTerm2, Windows Terminal, most Linux terminals)
- In environments without a real TTY (CI, Docker), use `--no-tui`

### CSV latency columns show zero or whole-millisecond values

- This can happen when using an old CSV file from before sub-millisecond support was added.
  Start a fresh run (rename or move the old CSV file) to get float-precision columns.
- Ensure the scenario has `[metrics]` configured — without a `[metrics]` section the
  default bucket layout is used and all columns are present.

### CSV file not created

- Check the directory in `csv_path` exists and is writable
- Confirm the `[report]` section is present in the scenario TOML (it is optional; omitting
  it disables CSV output)

### Sequence counter does not reset after Restart

- `Restart()` resets the sequence to its configured `start` value. If you see stale values,
  ensure you used `R` (uppercase restart) in the TUI, not `r` (resume).

### `a` picker shows an empty list

- The Add scenario picker only lists `.toml` files that are **not already loaded**. If all
  files in `--config-dirs` are already running, the picker will say "no candidates".
- Pass additional directories with `--config-dirs` to make more files discoverable.

### Manual does not display in the terminal

- Ensure your terminal emulator supports ANSI escape codes (virtually all modern terminals do)
- If colours look wrong, the glamour renderer may have chosen an incompatible theme; try
  `loadtest manual --raw | less` to read the plain Markdown source

### Email row shows "FAILED" in the Overview tab

- Inspect the error suffix in the TUI row; common causes:
  - `dial …: connection refused` — wrong `smtp_host` / `smtp_port`, server down
  - `STARTTLS: …` — the server didn't advertise STARTTLS but credentials were
    provided. Loadtest refuses to send the password in cleartext unless the
    target is loopback. Either remove the credentials or point at a port that
    speaks STARTTLS (usually 587).
  - `auth: …` — bad username / password. For Gmail and similar, generate an
    app-specific password; the regular account password won't work.
  - `render body: …` — typo in the body template. The subject template falls
    back to a literal on error, but a bad body fails the whole send.

### Email is configured but nothing arrives

- The scenario must reach **DONE** or **ERROR**. Pressing `s` (stop) doesn't
  trigger the email — that's user-initiated, not a terminal lifecycle event.
- Check the `on = […]` list; it defaults to `["done", "error"]` but a custom
  list may have excluded the event your scenario hit.
- Look for an error in the Overview tab. If you see `📧 sent at …` but the
  inbox is empty, check the recipient's spam folder; the default
  `noreply@livecharge.local` From: address is suspicious to many providers.
  Set a real `--mail-from` for production use.

### Why are my HTTPS requests using HTTP/1.1?

Check the Overview tab's PROTOCOL field. If it reads `HTTP/1.1 (negotiated to h1.1)`, the server didn't advertise `h2` in ALPN. If it reads `HTTPS (h1.1 forced)`, you set `http2 = false`.

### Why was my expr predicate rejected at startup?

Your expression failed to compile. The Overview tab shows the exact
error from expr-lang; common causes are an unbalanced `(`, a missing
right-hand side after an operator, or referencing a function that
doesn't exist in the built-ins table above.
