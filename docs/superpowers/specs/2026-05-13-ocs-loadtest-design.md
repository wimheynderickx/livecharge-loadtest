# OCS LoadTest — Design Specification

**Project:** Livecharge OCS LoadTest  
**Binary:** `loadtest`  
**Module:** `livecharge/loadtest`  
**Go version:** 1.22  
**Date:** 2026-05-13  
**Status:** Approved

---

## 1. Overview

`loadtest` is a standalone Go CLI load-testing tool for OCS components. It sends JSON payloads over NATS or HTTP(S), supports multi-step sessions with conditional branching, measures latency and throughput, and renders a live TUI dashboard. Configuration is entirely TOML-based. A built-in mock server enables self-contained testing without any OCS dependencies.

**Out of scope for v1:** coordinated multi-node execution (single process is expected to sustain ≥500 msg/sec).

---

## 1a. Code Style & Readability

All Go source files must be written to be readable by Go novices:

- Every exported type, function, and method has a doc comment explaining **what it does and why**.
- Every non-trivial block of logic has an inline comment explaining the intent, not just restating the code.
- Package-level `doc.go` files explain the purpose of each package and how it fits into the overall system.
- Avoid clever one-liners; prefer explicit multi-line forms.

This is a hard requirement, not a style preference. Code review must reject unexplained logic.

See `docs/repository-layout.md` for per-package descriptions.  
See `docs/operational-manual.md` for CLI usage.

---

## 2. Repository Layout

```
loadtest/
├── cmd/loadtest/
│   └── main.go                  # CLI entry point (cobra)
├── internal/
│   ├── config/                  # TOML parsing and validation
│   ├── engine/                  # scenario runner, session manager, load generator
│   ├── transport/
│   │   ├── transport.go         # Transport interface + Request/Response types
│   │   ├── nats/                # NATS implementation
│   │   └── http/                # HTTP/HTTPS implementation
│   ├── template/                # Go text/template wrapper + context resolution
│   ├── metrics/                 # HDR histograms, counters, throughput tracker
│   ├── tui/                     # Bubble Tea dashboard
│   ├── report/                  # CSV writer
│   └── mockserver/              # Mock NATS subscriber / HTTP server
├── scenarios/                   # Example TOML files
├── Dockerfile
└── go.mod
```

---

## 3. Transport Layer

### 3.1 Interface

```go
// internal/transport/transport.go

type Request struct {
    Subject  string            // NATS subject or HTTP path
    Method   string            // HTTP only (GET, POST, …)
    Headers  map[string]string // applied to both NATS and HTTP
    Body     []byte
    Timeout  time.Duration
}

type Response struct {
    Body       []byte
    Headers    map[string]string // HTTP response headers
    StatusCode int               // HTTP only
    Meta       map[string]string // NATS reply headers (nats.Msg.Header)
    Latency    time.Duration
}

type Transport interface {
    Send(ctx context.Context, req Request) (Response, error)
    Close() error
}
```

### 3.2 NATS Transport

| Feature | Detail |
|---|---|
| Request/reply | `nats.Conn.RequestMsgWithContext` — built-in inbox reply |
| Fire-and-forget | `nats.Conn.PublishMsg` — no reply, no latency recorded |
| Request headers | `nats.Msg.Header` |
| Response headers | `nats.Msg.Header` → `Response.Meta` (accessible as `meta/X` in JSON-path) |
| Auth: none | plain `nats.Connect(url)` |
| Auth: userpass | `nats.UserInfo(user, pass)` option |

### 3.3 HTTP/HTTPS Transport

| Feature | Detail |
|---|---|
| Client | Shared `http.Client` with connection pool per scenario |
| TLS | Standard `https://` URL; no mTLS in v1 |
| Auth: none | no extra headers |
| Auth: basic | `Authorization: Basic <base64(user:pass)>` |
| Auth: jwt | `Authorization: Bearer <token>` |

### 3.4 Auth Extensibility

New auth types are added by introducing a new `type` value in `[transport.auth]` and a corresponding option/header injection in the transport constructor. The engine and session runner are unaffected.

---

## 4. Template & Context System

### 4.1 Template Engine

Go `text/template` (`text/template` stdlib). Templates are TOML string values; standard `{{}}` syntax.

### 4.2 Context Namespaces

| Namespace | Description | Example |
|---|---|---|
| `.ctx.X` | Static or generated value, initialised once per session | `{{.ctx.terminalId}}` |
| `.session.X` | Value extracted from a previous step reply | `{{.session.chargeRef}}` |
| `.extracted.X` | Incoming request field (mock server only) | `{{.extracted.chargeId}}` |

### 4.3 Context Value Types

```toml
[context]
terminalId = "TERM-001"        # static string / int / bool

[context.chargeId]
type  = "sequence"             # global atomic counter across all concurrent sessions
start = 1000
step  = 1

[context.msisdn]
type   = "random_pick"         # random element from list each session
values = ["31612345678", "31698765432"]

[context.amount]
type = "random_range"          # random integer in [min, max] each session
min  = 100
max  = 9999
```

Sequence counters are global atomics — each of the `concurrency` sessions draws the next unique value.

### 4.4 JSON-Path Extraction

Path notation: slash-separated keys traversing parsed JSON.

| Prefix | Source |
|---|---|
| `response/field/subfield` | JSON body field (NATS or HTTP) |
| `body/field` | HTTP JSON body (explicit prefix) |
| `header/X-Header-Name` | HTTP response header |
| `status` | HTTP status code as string |
| `meta/X-Header-Name` | NATS reply header |

Extracted values are stored as strings in `.session`. Numeric predicates parse on-the-fly.

### 4.5 Predicate Evaluation

```go
type Predicate struct {
    Name     string   // accounting key
    Field    string   // "session.status", "session.amount", "status"
    Op       string   // eq | ne | contains | gt | lt
    Value    string
    NextStep string   // named step to jump to; "" = end session
}
```

Predicates are evaluated in order; first match wins. If none match, execution continues to the next step in the list. A matched predicate records its timestamp and latency into the per-predicate histogram.

### 4.6 Pre-calculation

At startup, templates that use **only static `.ctx` values** (no sequence/random generators, no `.session` references) are rendered once and the bytes reused across all sessions. The engine detects this by inspecting the context definition: if all referenced `.ctx` keys map to static scalars and no `.session` keys appear, the template is pre-rendered. Sequence and random generators produce a different value each session and are never pre-calculated.

---

## 5. Scenario Execution Engine

### 5.1 Component Overview

```
ScenarioRunner
  ├── LoadGenerator       # token-bucket rate limiter + concurrency pool
  ├── SessionPool         # goroutine-per-session, restarts on completion
  │     └── Session       # step loop
  │           ├── ContextFactory.Snapshot()
  │           ├── template.Render()
  │           ├── transport.Send()
  │           ├── extractor.Extract() → .session
  │           └── predicate.Evaluate() → next step
  └── MetricsCollector    # receives StepResult events (channel-based)
```

### 5.2 Session Lifecycle

```
START
  ├─ Snapshot .ctx (static + generated values)
  └─ LOOP:
       ├─ Render template → JSON bytes
       ├─ transport.Send() [with step or scenario timeout]
       │     ├─ OK  → Extract → Evaluate predicates → pick next step
       │     └─ timeout/error → record error, end session
       └─ next_step == "" → END session
  └─ Pool restarts session immediately to maintain concurrency
```

### 5.3 Load Generator

- Rate limiting via `golang.org/x/time/rate` token bucket
- `rate = 0` → `rate.InfRate` (as fast as possible)
- Stops on: `total_messages` reached, `duration` elapsed, OS signal, or TUI cancel
- Elapsed duration is banked on `Stop()` and deducted on `Resume()`

### 5.4 Scenario Lifecycle (per scenario in a suite)

**States:** `IDLE → RUNNING → STOPPED → RUNNING` (resume) or `DONE` or `ERROR`

```go
type ScenarioRunner interface {
    Start()   error
    Stop()    error    // drains in-flight sessions; banks elapsed time + message count
    Resume()  error    // continues from banked state
    Restart() error    // resets counters, metrics, elapsed time; starts fresh
    State()   State
    Metrics() Snapshot
}
```

| State preserved | Stop → Resume | Restart |
|---|---|---|
| Sequence counters | continued | reset to `start` |
| Accumulated metrics | continued | cleared |
| Remaining messages | deducted | full `total_messages` |
| Remaining duration | elapsed banked | full `duration` |
| In-flight sessions | drained | dropped |

### 5.5 Multiple Simultaneous Scenarios

Each `ScenarioRunner` runs in its own goroutine. The TUI collects metrics from all runners via a fan-in channel. Stopping one scenario does not affect others. A global cancel (Ctrl-C) stops all runners gracefully.

---

## 6. TOML Configuration Format

### 6.1 Scenario File

```toml
[scenario]
name        = "charge-flow"
description = "OCS charging session happy flow"

# Transport
[transport]
type = "nats"                   # "nats" | "http" | "https"
url  = "nats://localhost:4222"

[transport.auth]
type     = "userpass"           # "none" | "userpass" | "basic" | "jwt"
username = "ocs_user"
password = "secret"

# Context variables
[context]
terminalId = "TERM-001"

[context.chargeId]
type  = "sequence"
start = 1000
step  = 1

[context.msisdn]
type   = "random_pick"
values = ["31612345678", "31698765432"]

[context.amount]
type = "random_range"
min  = 100
max  = 9999

# Load parameters
[load]
rate             = 500          # msg/sec; 0 = as fast as possible
total_messages   = 100000       # 0 = unlimited
duration         = "10m"        # 0 = unlimited; Go duration string
concurrency      = 50
response_timeout = "2s"         # default 2s; also sets HDR histogram upper bound

# Metrics
[metrics]
percentiles = [50, 75, 90, 95, 99, 99.9]

# CSV reporting (optional)
[report]
csv_path         = "./results/charge-flow-{timestamp}.csv"  # {timestamp} replaced at scenario start
timestamp_format = "2006-01-02T15-04-05"                    # Go time layout string (default shown)
overwrite        = true                                      # true = truncate existing file (default); false = append
flush_interval   = "10s"

# Steps
[[step]]
name    = "initiate-charge"
subject = "ocs.charge.initiate"   # NATS subject or HTTP path
template = """
{
  "terminalId": "{{.ctx.terminalId}}",
  "chargeId":   {{.ctx.chargeId}},
  "msisdn":     "{{.ctx.msisdn}}",
  "amount":     {{.ctx.amount}}
}
"""

  [[step.header]]
  name  = "X-Correlation-Id"
  value = "{{.ctx.chargeId}}"

  [[step.extract]]
  field = "chargeRef"
  path  = "response/chargeRef"

  [[step.extract]]
  field = "status"
  path  = "response/status"

  [[step.predicate]]
  name      = "happy-flow"
  field     = "session.status"
  op        = "eq"
  value     = "APPROVED"
  next_step = "confirm-charge"

  [[step.predicate]]
  name      = "error-flow"
  field     = "session.status"
  op        = "eq"
  value     = "REJECTED"
  next_step = ""                # end session early

[[step]]
name    = "confirm-charge"
subject = "ocs.charge.confirm"
template = """
{
  "chargeRef":  "{{.session.chargeRef}}",
  "terminalId": "{{.ctx.terminalId}}"
}
"""
```

**Step-level timeout override:**

```toml
[[step]]
name             = "slow-enrichment"
subject          = "ocs.enrich"
response_timeout = "10s"           # overrides [load] response_timeout for this step only
template         = """..."""
```

If `response_timeout` is omitted on a step, it inherits the value from `[load] response_timeout` (default `"2s"`).

**HTTP-specific step fields:**

```toml
[[step]]
name     = "create-charge"
method   = "POST"
path     = "/v1/charges"
template = """{ "amount": {{.ctx.amount}} }"""

  [[step.extract]]
  field = "chargeId"
  path  = "body/id"              # "body/X" | "header/X" | "status"
```

**Fire-and-forget (NATS only):**

```toml
[[step]]
name             = "emit-event"
subject          = "ocs.events"
fire_and_forget  = true
template         = """{ "event": "charge-started" }"""
```

### 6.2 Suite File

```toml
[suite]
name = "Full Regression"

[[suite.scenario]]
file = "scenarios/charge-flow.toml"

[[suite.scenario]]
file = "scenarios/auth-flow.toml"
```

---

## 7. Metrics & Reporting

### 7.1 MetricsCollector (per scenario)

- **Counters (atomic):** `sessions_created`, `messages_sent`, `messages_received`, `errors`
- **Throughput:** sliding 1-second window → current msg/sec
- **HDR histogram:** one global + one per named predicate; upper bound = `response_timeout`; 3 significant figures; library: `github.com/HdrHistogram/hdrhistogram-go`
- **Predicate accounting:** per name: `count`, `first_match_at`, `last_match_at`, `avg_latency`
- `Snapshot()` returns a read-consistent view; TUI polls every 250 ms

### 7.2 CSV Report

```csv
timestamp,scenario,messages_sent,messages_received,errors,msg_per_sec,p50_ms,p95_ms,p99_ms,predicate_happy_count,predicate_error_count
2026-05-13T21:00:00Z,charge-flow,5000,4998,2,487.3,12,48,112,4950,48
```

- One row per `flush_interval`; header written once
- File opened on `Start()`, closed on `Stop()` / `Done()`
- Percentile columns derived from configured `[metrics] percentiles`
- `csv_path` supports a `{timestamp}` placeholder replaced with the scenario start time, formatted by `timestamp_format` (Go reference time layout, default `"2006-01-02T15-04-05"`)
- `overwrite = true` (default): truncates an existing file on open; `overwrite = false`: appends to an existing file

---

## 8. TUI Dashboard

Library: **Bubble Tea** (`github.com/charmbracelet/bubbletea`) + Lip Gloss for styling.

### 8.1 Layout

```
┌─ header: binary name | suite name | elapsed | [Q]quit ──────────────────┐
│ ┌── sidebar (220px) ─┬── tabbed detail panel ───────────────────────────┐│
│ │ SCENARIOS          │ [1]Overview [2]Latency [3]Predicates [4]Log      ││
│ │ ▶ charge-flow      │                                                   ││
│ │   RUNNING 487m/s   │  (tab content)                                    ││
│ │   [S]top [↺]restart│                                                   ││
│ │ ○ auth-flow        │                                                   ││
│ │   STOPPED          │                                                   ││
│ │   [R]esume [↺]     │                                                   ││
│ │ ○ refund-flow IDLE │                                                   ││
│ │   [▶]start         │                                                   ││
│ │────────────────────│                                                   ││
│ │ SUITE TOTALS       │                                                   ││
│ │ sent:   86,631     │                                                   ││
│ │ rcvd:   86,628     │                                                   ││
│ │ errors: 3          │                                                   ││
│ └────────────────────┴───────────────────────────────────────────────────┘│
└───────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Tab Content

**[1] Overview:** 4 KPI boxes (msg/sec, sent, received, errors) + latency percentile list + progress bar with ETA.

**[2] Latency:** Full HDR histogram with configurable bucket breakdown; predicate selector to switch histogram view.

**[3] Predicates:** Table — name, count, %, first match timestamp, last match timestamp, avg latency.

**[4] Log:** Scrolling tail of errors and state change events.

### 8.3 Keyboard Shortcuts

| Key | Action |
|---|---|
| `↑` / `↓` | Select scenario in sidebar |
| `1`–`4` | Switch detail tab |
| `s` | Stop selected scenario |
| `r` | Resume selected scenario |
| `R` | Restart selected scenario |
| `Space` | Start selected scenario (if IDLE) |
| `q` | Quit (stops all running scenarios) |

---

## 9. CLI Interface

```bash
loadtest run   scenario.toml [scenario2.toml …]   # run scenario(s)
loadtest run   suite.toml                          # run suite
loadtest validate  config.toml                     # validate without running
loadtest mock  mock-config.toml                    # start mock server
loadtest version
```

### 9.1 `run` Flags

| Flag | Default | Description |
|---|---|---|
| `--config-dirs` | `.` | Extra dirs to search for TOML files; also used to resolve relative `file =` paths in suite files |
| `--no-tui` | false | Print stats to stdout (CI / headless mode) |
| `--log-file` | — | Write JSON structured logs to file |

### 9.2 `--no-tui` Output

```
2026-05-13T21:04:17Z charge-flow RUNNING sent=74231 rcvd=74228 err=3 msg/s=487 p50=12ms p99=112ms
```

### 9.3 Docker

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o loadtest ./cmd/loadtest

FROM alpine:3.19
COPY --from=builder /app/loadtest /usr/local/bin/
ENTRYPOINT ["loadtest"]
```

Scenarios mounted as volume:
```bash
docker run -v ./scenarios:/scenarios loadtest run /scenarios/charge-flow.toml
```

---

## 10. Mock Server

### 10.1 Overview

`loadtest mock mock-config.toml` starts a lightweight NATS subscriber or HTTP server for self-contained testing. Lives in `internal/mockserver/`; no imports from `engine/` or `tui/`.

### 10.2 Mock Config

```toml
[mock]
type = "nats"                         # "nats" | "http"
url  = "nats://localhost:4222"

[mock.auth]
type = "none"

[[mock.endpoint]]
subject   = "ocs.charge.initiate"     # NATS subject; HTTP: method + path
fail_rate = 0.05                      # 5% return fail_response

  [[mock.endpoint.extract]]
  field = "chargeId"                  # extract from incoming JSON body
  path  = "chargeId"                  # same JSON-path notation as load tool

  [mock.endpoint.ok_response]
  template = """
  {
    "status":    "APPROVED",
    "chargeRef": "REF-{{.extracted.chargeId}}"
  }
  """

  [mock.endpoint.fail_response]
  template = """
  {
    "status": "REJECTED",
    "reason": "INSUFFICIENT_BALANCE"
  }
  """

[[mock.endpoint]]
subject   = "ocs.charge.confirm"
fail_rate = 0.0

  [mock.endpoint.ok_response]
  template = """{ "status": "CONFIRMED" }"""

  [mock.endpoint.fail_response]
  template = """{ "status": "ERROR" }"""
```

`{{.extracted.X}}` — field extracted from the incoming request body, available in response templates.

---

## 11. Requirements Traceability

| Requirement | Covered by |
|---|---|
| OCS-LOAD-MAIN-100 | Transport layer (§3), NATS + HTTP |
| OCS-LOAD-MAIN-110 | Template engine — Go text/template (§4) |
| OCS-LOAD-MAIN-111 | Context resolution: static, range, session extraction (§4.2–4.4) |
| OCS-LOAD-MAIN-120 | NATS fire-and-forget via `fire_and_forget = true` (§3.2, §6.1) |
| OCS-LOAD-TEST-200 | ScenarioRunner (§5) |
| OCS-LOAD-TEST-210 | Single-step and multi-step sessions (§5.2) |
| OCS-LOAD-TEST-220 | `total_messages = 1` for one-off; load params for sustained (§6.1) |
| OCS-LOAD-TEST-230 | MetricsCollector: msg/sec + latency (§7.1) |
| OCS-LOAD-TEST-231 | HDR histogram per scenario and per predicate (§7.1) |
| OCS-LOAD-TEST-240 | TUI Overview tab + `--no-tui` stdout stats (§8, §9.2) |
| OCS-LOAD-TEST-241 | `Stop()` / Ctrl-C cancellation (§5.4) |
| OCS-LOAD-SCENARIO-300 | JSON-path extraction from reply body (§4.4) |
| OCS-LOAD-SCENARIO-301 | HTTP status + header extraction via `status` / `header/X` paths (§4.4) |
| OCS-LOAD-SCENARIO-302 | NATS metadata via `meta/X` path (§4.4) |
| OCS-LOAD-SCENARIO-310 | Predicates with `op`, `field`, `next_step` (§4.5) |
| OCS-LOAD-SCENARIO-311 | Per-predicate accounting: count, first/last match (§7.1) |
| OCS-LOAD-SCENARIO-312 | Per-predicate HDR histogram (§7.1) |
| OCS-LOAD-SCENARIO-320 | TOML configuration (§6) |
| OCS-LOAD-RUN-400 | Multiple simultaneous ScenarioRunners (§5.5) |
| OCS-LOAD-RUN-410 | Goroutine pool + token bucket; standalone Go target ≥500 msg/sec (§5.3) |
| OCS-LOAD-RUN-420 | No OCS library dependencies; JSON text templates only (§4.1) |
| OCS-LOAD-RUN-430 | Pre-calculation of static templates at startup (§4.6) |
| OCS-LOAD-RUN-440 | CSV report with `flush_interval`; no in-memory accumulation limit (§7.2) |
| OCS-LOAD-RUN-450 | Docker multi-stage build (§9.3) |
| OCS-LOAD-RUN-460 | `internal/` can be promoted to `pkg/` when Maven integration is needed |

---

## 12. Key Dependencies

| Package | Purpose |
|---|---|
| `github.com/nats-io/nats.go` | NATS client |
| `github.com/charmbracelet/bubbletea` | TUI framework |
| `github.com/charmbracelet/lipgloss` | TUI styling |
| `github.com/HdrHistogram/hdrhistogram-go` | Latency histograms |
| `github.com/BurntSushi/toml` | TOML parsing |
| `github.com/spf13/cobra` | CLI commands |
| `golang.org/x/time/rate` | Token bucket rate limiter |
