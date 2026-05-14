# Repository Layout

This document explains what every directory and file in the `ocs-loadtest` repository is for,
and how the pieces fit together. It is intended for developers who are new to the project or
new to Go.

---

## Top-Level Overview

```
ocs-loadtest/
├── cmd/                    ← runnable programs (there is only one: the CLI)
├── internal/               ← all application logic, not importable from outside this module
├── scenarios/              ← example TOML configuration files you can use as a starting point
├── docs/                   ← documentation (you are here)
├── Dockerfile              ← builds a Docker image for deployment
└── go.mod                  ← Go module definition; lists direct dependencies
```

In Go, `internal/` is a special directory name. The Go compiler enforces that packages inside
`internal/` can only be imported by code within the same module. This is intentional: it means
the packages here are **implementation details**, not a public API. If you ever want to make
the engine importable by other Go projects (e.g. for the future Maven/Java integration), you
would rename `internal/` to `pkg/`.

---

## `cmd/ocs-loadtest/`

```
cmd/ocs-loadtest/
└── main.go
```

**What it is:** The entry point of the program. When you run `go build ./cmd/ocs-loadtest`
you get the `ocs-loadtest` binary.

**What it does:** It wires all the internal packages together and hands control to the CLI
framework (Cobra). `main.go` is intentionally thin — it creates the root command, registers
sub-commands (`run`, `validate`, `mock`, `version`), and calls `Execute()`. All real logic
lives in `internal/`.

**What it does NOT do:** It contains no business logic. If you find yourself writing anything
more complex than flag parsing and dependency wiring here, it belongs in `internal/` instead.

---

## `internal/config/`

```
internal/config/
├── config.go       ← top-level Config, ScenarioConfig, SuiteConfig structs
├── context.go      ← ContextValueConfig (static, sequence, random_range, random_pick)
├── load.go         ← LoadConfig (rate, concurrency, timeout, duration)
├── step.go         ← StepConfig, ExtractConfig, PredicateConfig, HeaderConfig
├── mock.go         ← MockConfig, MockEndpointConfig
└── validate.go     ← cross-field validation logic (e.g. histogram_max cannot exceed timeout)
```

**What it is:** TOML parsing and validation. This package reads `.toml` files from disk and
turns them into typed Go structs that the rest of the application uses.

**Why a separate package:** Keeping config parsing isolated means the rest of the application
never reads files directly. It depends only on the structs defined here. This makes testing
easy — you can construct a `ScenarioConfig` in a test without touching the filesystem.

**Key types:**
- `ScenarioConfig` — everything in a scenario TOML file
- `SuiteConfig` — a suite TOML file that references multiple scenario files
- `StepConfig` — one `[[step]]` block, including its headers, extractions, and predicates

---

## `internal/engine/`

```
internal/engine/
├── engine.go           ← ScenarioRunner interface and State type
├── runner.go           ← ScenarioRunner implementation: Start/Stop/Resume/Restart
├── session.go          ← one Session: executes the step loop for a single virtual user
├── load_generator.go   ← token-bucket rate limiter + goroutine pool management
└── precalc.go          ← detects and pre-renders fully static templates at startup
```

**What it is:** The core execution engine. This is where load testing actually happens.

**How it works at a high level:**

1. `ScenarioRunner` is created from a `ScenarioConfig`. It owns a `LoadGenerator` and a
   `MetricsCollector`.
2. `LoadGenerator` maintains a pool of `concurrency` goroutines. Each goroutine runs one
   `Session` at a time and immediately starts a new one when the previous session ends.
3. A `Session` steps through the `[[step]]` list: render template → send via transport →
   extract fields from reply → evaluate predicates → pick next step → repeat.
4. After each step, the session sends a `StepResult` to the `MetricsCollector`.

**Lifecycle methods on `ScenarioRunner`:**
- `Start()` — allocates the goroutine pool and begins sending
- `Stop()` — signals the pool to drain in-flight sessions, then waits; banks elapsed time
- `Resume()` — restarts the pool from the banked state (keeps counters and metrics)
- `Restart()` — resets everything and calls `Start()` fresh

---

## `internal/transport/`

```
internal/transport/
├── transport.go    ← Transport interface, Request and Response types
├── nats/
│   └── nats.go     ← NATS implementation of Transport
└── http/
    └── http.go     ← HTTP/HTTPS implementation of Transport
```

**What it is:** The network layer. Everything that touches the wire lives here.

**The central abstraction:**

```go
type Transport interface {
    Send(ctx context.Context, req Request) (Response, error)
    Close() error
}
```

The engine only knows about `Transport`. It does not know whether it is talking to NATS or
HTTP. This means you can add a new protocol (e.g. gRPC, Kafka) by adding a new subdirectory
here without touching any other package.

**`transport/nats/`** — wraps the `nats.go` client library. Supports:
- Request/reply using `nats.Conn.RequestMsgWithContext` (built-in inbox reply subject)
- Fire-and-forget using `nats.Conn.PublishMsg` (no reply expected)
- NATS message headers (`nats.Msg.Header`) for both outgoing and incoming messages
- Auth: plain (no credentials) and username/password

**`transport/http/`** — wraps the standard `net/http` package. Supports:
- A shared `http.Client` with connection pooling (one client per scenario, reused across
  all concurrent sessions — important for performance)
- Auth: none, HTTP Basic, and JWT Bearer token

---

## `internal/template/`

```
internal/template/
├── resolver.go     ← ContextFactory: builds and snapshots the .ctx namespace per session
├── renderer.go     ← wraps Go text/template; renders a template string with a Context
└── extractor.go    ← JSON-path extraction from response bodies, headers, and NATS metadata
```

**What it is:** Everything related to template rendering and value resolution.

**The three concerns:**

1. **Context factory (`resolver.go`)** — reads the `[context]` block from TOML and creates
   generators. For each new session, `Snapshot()` is called to produce an initial `.ctx` map:
   - Static values are copied as-is
   - `sequence` generators increment a global atomic counter (safe for concurrent sessions)
   - `random_range` and `random_pick` produce a new random value per session

2. **Renderer (`renderer.go`)** — takes a Go `text/template` string and a `Context`
   (holding both `.ctx` and `.session`) and returns rendered JSON bytes. Wraps
   `text/template.Execute` with error context so failures are easy to diagnose.

3. **Extractor (`extractor.go`)** — given a `Response` and a path like `response/chargeRef`
   or `header/X-Charge-Id`, navigates the parsed JSON or header map and returns the string
   value. Stores it in the session's `.session` map for use in subsequent step templates.

---

## `internal/metrics/`

```
internal/metrics/
├── collector.go    ← MetricsCollector: receives StepResult events, updates all stats
├── histogram.go    ← HDR histogram wrapper (one per scenario, one per predicate)
├── snapshot.go     ← Snapshot type: a point-in-time read-consistent view of all stats
└── throughput.go   ← sliding 1-second window for current msg/sec calculation
```

**What it is:** All statistics collection. Nothing here talks to the network or renders UI.

**Why HDR histograms:** A standard average hides the tail. HDR (High Dynamic Range) histograms
track the full distribution of latencies with high precision and low memory usage. The library
used is `github.com/HdrHistogram/hdrhistogram-go`.

**Key design:** The `MetricsCollector` is the only writer; all other packages only call
`Snapshot()` which returns a read-consistent copy. This avoids lock contention between the
engine (high-frequency writer) and the TUI (250 ms reader).

**Per-predicate histograms:** When a predicate matches, the step's latency is recorded into
that predicate's histogram in addition to the global one. This lets you see, for example,
that `happy-flow` latency is 12 ms while `error-flow` latency is 8 ms.

---

## `internal/tui/`

```
internal/tui/
├── app.go          ← root Bubble Tea model: wires sidebar + detail panel + header
├── sidebar.go      ← scenario list with state badges and action buttons
├── tabs.go         ← tab bar and tab switching logic
├── overview.go     ← Tab 1: KPI boxes, latency percentile list, progress bar
├── latency.go      ← Tab 2: HDR histogram rendered as ASCII bar chart
├── predicates.go   ← Tab 3: predicate accounting table
├── log.go          ← Tab 4: scrolling error/event log
└── styles.go       ← Lip Gloss colour and layout constants
```

**What it is:** The terminal user interface. Built with Bubble Tea, a Go TUI framework that
uses an Elm-style architecture: `Model → Update(msg) → View()`.

**Layout:** Split pane. Left sidebar (220 columns wide) lists all scenarios with their
current state and action buttons. The right panel shows tabs for the selected scenario:
Overview, Latency histogram, Predicates table, and Log.

**Update loop:** A `ticker` fires every 250 ms and triggers a call to `Snapshot()` on all
active `ScenarioRunner` instances. The model stores the snapshots and Bubble Tea re-renders
the view. This keeps the TUI decoupled from the engine — the engine never calls into the TUI.

**Keyboard shortcuts** (defined in `app.go`):

| Key | Action |
|-----|--------|
| `↑` / `↓` | Move scenario selection in sidebar |
| `1`–`4` | Switch tab in detail panel |
| `s` | Stop selected scenario |
| `r` | Resume selected scenario |
| `R` (shift) | Restart selected scenario |
| `Space` | Start selected scenario (if IDLE) |
| `q` | Quit — stops all running scenarios gracefully |

---

## `internal/report/`

```
internal/report/
└── csv.go     ← CSVWriter: opens file, writes header row, appends stat rows on a ticker
```

**What it is:** Optional CSV export of periodic statistics snapshots.

**How it works:** If `[report] csv_path` is set in the scenario TOML, a `CSVWriter` is
created when the scenario starts. Every `flush_interval` (default `10s`) it calls
`Snapshot()` on the `MetricsCollector` and appends one row to the file. The file is kept
open for the lifetime of the scenario so writes are buffered efficiently. It is closed
cleanly on `Stop()` or when the scenario completes.

**CSV columns:** timestamp, scenario name, messages sent/received/errors, msg/sec, and one
column per configured percentile (e.g. `p50_ms`, `p95_ms`, `p99_ms`). Predicate counts are
appended as additional columns named `predicate_<name>_count`.

---

## `internal/mockserver/`

```
internal/mockserver/
├── server.go       ← MockServer: starts NATS subscriber or HTTP listener from MockConfig
├── handler.go      ← per-endpoint request handler: extract → pick ok/fail → render reply
└── extractor.go    ← reuses internal/template extractor for incoming request JSON
```

**What it is:** A minimal test double for OCS components. Used to validate the load tool
itself without needing a real OCS system.

**What it does:** For each `[[mock.endpoint]]` in the mock TOML file:
- Subscribes to a NATS subject **or** registers an HTTP path
- When a message arrives, extracts configured JSON fields from the body
- Randomly selects `ok_response` or `fail_response` based on `fail_rate`
- Renders the selected template with `{{.extracted.X}}` values and sends the reply

**What it does NOT do:** It does not validate, authenticate, persist state, or do anything
else. It is intentionally minimal. Its only job is to reply fast enough for load testing.

---

## `scenarios/`

Example TOML files that demonstrate common patterns:

| File | Demonstrates |
|------|-------------|
| `scenarios/nats-single-step.toml` | One-shot NATS request/reply with no session state |
| `scenarios/nats-session.toml` | Multi-step NATS session with context extraction |
| `scenarios/http-basic-auth.toml` | HTTP POST with Basic auth |
| `scenarios/http-jwt.toml` | HTTP with JWT Bearer token |
| `scenarios/fire-and-forget.toml` | NATS fire-and-forget (no reply expected) |
| `scenarios/suite-example.toml` | Suite file referencing multiple scenario files |
| `mock/nats-mock.toml` | Mock server config matching `nats-session.toml` |
| `mock/http-mock.toml` | Mock server config matching `http-basic-auth.toml` |

---

## `docs/`

| File | Contents |
|------|----------|
| `docs/repository-layout.md` | This file — what is where and why |
| `internal/manual/manual.md` | Operational manual — install, configure, run. Embedded into the binary (`go:embed`) and shown by `loadtest manual` and the TUI `m` shortcut. |
| `docs/superpowers/specs/2026-05-13-ocs-loadtest-design.md` | Full design specification |

---

## How the Packages Connect

```
main.go
  │
  ├── config/          reads TOML files → typed structs
  │
  ├── engine/          orchestrates sessions
  │     ├── template/  renders templates + manages context
  │     ├── transport/ sends/receives over NATS or HTTP
  │     └── metrics/   records latency + counters
  │
  ├── tui/             renders the dashboard (reads metrics via Snapshot)
  ├── report/          writes CSV (reads metrics via Snapshot)
  └── mockserver/      standalone mock (uses template/extractor)
```

The dependency arrows only point **downward and inward**. The TUI never imports the engine;
it only reads `metrics.Snapshot`. The engine never imports the TUI. This makes each package
independently testable and replaceable.
