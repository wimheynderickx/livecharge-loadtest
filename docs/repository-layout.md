# Repository Layout

This document explains what every directory and file in the `loadtest`
repository is for, and how the pieces fit together. It is intended for
developers who are new to the project or new to Go.

---

## Top-Level Overview

```
sequana2-perf-test/
├── cmd/                    ← runnable programs (just one: the CLI)
├── internal/               ← application logic, not importable from outside this module
├── scenarios/              ← example scenario / suite TOML files
├── mock/                   ← example mock-server TOML files
├── docs/                   ← documentation (you are here)
├── mail-config.toml        ← optional shared email config (gitignored)
├── Makefile                ← build / test / run shortcuts
├── Dockerfile              ← container build
└── go.mod                  ← Go module definition (module name: livecharge/loadtest)
```

The Go compiler treats `internal/` specially: packages inside `internal/`
can only be imported by code within the same module. This makes them
implementation details rather than a public API.

---

## `cmd/loadtest/`

```
cmd/loadtest/
├── main.go         ← cobra root command, registers all sub-commands
├── run.go          ← `loadtest run` — runs scenarios with optional TUI
├── email.go        ← --mail-* flags, OnTerminal wiring, send orchestration
├── validate.go     ← `loadtest validate` — parse-only TOML check, CI-friendly
├── mock.go         ← `loadtest mock` — runs the mock NATS / HTTP server
├── help.go         ← `loadtest help` — curated overview vs. cobra's auto-help
├── manualcmd.go    ← `loadtest manual` — renders the embedded manual
└── version.go      ← `loadtest version`
```

**What it is:** The entry point. When you build `./cmd/loadtest` you get
the `loadtest` binary.

**What it does:** Wires the internal packages together and dispatches via
Cobra. Each `*.go` file in this directory holds one sub-command; the
files are intentionally thin — every non-trivial routine lives in
`internal/`.

`run.go` does the most: it loads scenarios, resolves the explicit /
implicit split for `--auto-start`, optionally loads `--mail-config`,
builds the mail registry, and hands off to either `runTUI` or
`runHeadless`. `email.go` carries the email feature's CLI flags and the
per-scenario lifecycle wiring (template context, async send, status).

---

## `internal/config/`

```
internal/config/
├── config.go       ← top-level structs: ScenarioConfig, SuiteConfig, etc.
├── context.go      ← ContextValueConfig (static, sequence, random_range, random_pick)
├── duration.go     ← TOML-friendly Duration wrapper around time.Duration
├── load.go         ← LoadConfig (rate, concurrency, response_timeout, duration)
├── load_files.go   ← LoadScenario / LoadSuite — TOML parsing entry points
├── mock.go         ← MockConfig, MockEndpointConfig (incl. fail_rate, no_answer_rate)
├── step.go         ← StepConfig, ExtractConfig, PredicateConfig, HeaderConfig
├── transport.go    ← TransportConfig + AuthConfig (none / userpass / basic / jwt)
├── validate.go     ← cross-field validation
└── doc.go          ← package overview
```

**What it is:** TOML parsing and validation. Reads `.toml` files from
disk and turns them into typed Go structs.

**Why a separate package:** Isolating config parsing means the rest of
the application never touches the filesystem. Tests can build a
`ScenarioConfig` in memory.

**Key types:**

- `ScenarioConfig` — everything in one scenario TOML, including the
  optional `[email]` block (typed as `*mail.Config`) and the `[report]`
  block for CSV output
- `SuiteConfig` — a suite file referencing multiple scenario files
- `StepConfig` — one `[[step]]` with its headers, extracts, predicates
- `MetricsConfig` — percentiles, `bucket_count` or `bucket_edges_ms`

---

## `internal/engine/`

```
internal/engine/
├── engine.go             ← ScenarioRunner interface + State enum
├── runner.go             ← Runner: Start/Stop/Resume/Restart + OnTerminal callback
├── session.go            ← one Session: executes the step loop for a virtual user
├── load_generator.go     ← token-bucket rate limiter + goroutine pool
├── precalc.go            ← pre-renders fully static templates at startup
├── transport_factory.go  ← builds the right transport from TransportConfig
├── runner_test.go        ← lifecycle tests (start/stop/resume/restart races)
└── doc.go
```

**What it is:** The core execution engine. Load testing happens here.

**Lifecycle (per scenario):**

1. `NewRunner(loaded)` allocates the transport, ContextFactory, Collector,
   and LoadGenerator from the config.
2. `Start()` spawns the goroutine pool. Each goroutine runs one Session
   at a time and immediately starts the next on completion.
3. A `Session` walks the `[[step]]` list: render template → send via
   transport → extract → evaluate predicates → pick next step.
4. After each step, the session submits a `StepResult` to the Collector.
5. A background watcher transitions the runner to `DONE` (on
   `total_messages` / `duration` reached) or `ERROR` (fatal transport
   failure). It fires `OnTerminal(state)` once after the final metrics
   are settled — used by `cmd/loadtest/email.go` to dispatch the
   post-run email.

**Two-context split for clean shutdown:** `sessionCtx` covers in-flight
sessions; `acceptCtx` is what workers consult to decide whether to take
on new work. Natural completion cancels only `acceptCtx` so existing
sessions can finish; external `Stop()` cancels both. This is why a
loadtest that hits its `total_messages` limit doesn't print spurious
`context canceled` errors for in-flight requests.

---

## `internal/transport/`

```
internal/transport/
├── transport.go    ← Transport interface, Request / Response types
├── doc.go
├── nats/
│   └── nats.go     ← NATS implementation
└── http/
    └── http.go     ← HTTP / HTTPS implementation
```

**What it is:** The network layer. The only place anything touches the
wire.

**Central interface:**

```go
type Transport interface {
    Send(ctx context.Context, req Request) (Response, error)
    Close() error
}
```

The engine knows about `Transport` only — adding a new protocol means
adding a sub-package, no changes elsewhere.

- **`transport/nats/`** — wraps the `nats.go` client. Request/reply
  via `RequestMsgWithContext`, fire-and-forget via `PublishMsg`, headers
  on both directions, auth: none / userpass.
- **`transport/http/`** — wraps `net/http`. One shared `http.Client`
  per scenario with connection pooling. Auth: none / Basic / JWT Bearer.

---

## `internal/template/`

```
internal/template/
├── resolver.go     ← ContextFactory: builds .ctx per session
├── renderer.go     ← wraps text/template; renders with a Context
├── extractor.go    ← JSON-path / header / status extraction from replies
├── precalc.go      ← pre-render detection for fully static templates
├── predicate.go    ← predicate evaluation against extracted session values
└── doc.go
```

**What it is:** Template rendering, context resolution, and reply
post-processing.

**Three concerns:**

1. **ContextFactory (`resolver.go`)** — reads `[context]` from TOML and
   builds generators. `Snapshot()` produces the `.ctx` map for a new
   session: static values copied as-is; `sequence` generators increment
   a global atomic counter; `random_range` / `random_pick` produce a
   fresh value per session.
2. **Renderer (`renderer.go`)** — Go `text/template` wrapper with
   diagnostic context on failure.
3. **Extractor (`extractor.go`)** — path navigation (`response/field`,
   `header/Name`, `status`, `meta/Header`) to populate `.session` for
   subsequent steps.

---

## `internal/metrics/`

```
internal/metrics/
├── collector.go     ← Collector: receives StepResult, updates all stats; log ring
├── histogram.go     ← HDR histogram wrapper (microsecond resolution)
├── buckets.go       ← bucket layout: auto from bucket_count OR manual edges
├── throughput.go    ← sliding 1-second window for current msg/sec
├── snapshot.go      ← Snapshot: point-in-time read-consistent view
├── collector_test.go
├── buckets_test.go
├── throughput_test.go
└── doc.go
```

**What it is:** All statistics collection. Nothing here touches the
network or renders UI.

**HDR histograms** record latencies in **microseconds** so
sub-millisecond values (e.g. `p50 = 0.135 ms`) display faithfully.
`buckets.go` lays out the histogram tab: either `bucket_count` evenly
spaced edges (75% finer-grained over the first half of the range), or
fully manual `bucket_edges_ms`.

**Throughput** keeps a 10×100 ms sliding window and tracks both the
current rate, the peak ever seen (`MaxMsgPerSec`), and the lifetime
average (`AvgMsgPerSec`). All three appear on the Overview tab.

**Log ring buffer:** in addition to the existing `LogCh`, the Collector
keeps a bounded slice of recent log lines (`LogTail(n)`). This lets the
email subsystem snapshot the scenario's log to attach to a run-summary
email without competing with the TUI for the channel.

---

## `internal/mail/`

```
internal/mail/
├── config.go              ← Config + TOML tags, Validate, FiresOn, ApplyDefaults
├── merge.go               ← LoadFile + Merge: scenario [email] → mail-config → CLI
├── template.go            ← TemplateContext, RenderText, RenderSubjectWithFallback
├── default_template.go    ← embedded plain-text default body template
├── sender.go              ← SMTP send (STARTTLS + PLAIN/LOGIN, port 587 by default)
├── status.go              ← thread-safe Status (Disabled / Pending / Sent / Failed)
├── config_test.go
├── merge_test.go
├── template_test.go
├── sender_test.go         ← uses github.com/mhale/smtpd as in-process mock SMTP
└── doc.go
```

**What it is:** Optional post-run email notification feature. Wired in
`cmd/loadtest/email.go`; the runner fires `OnTerminal` after reaching a
terminal state, the callback renders a `TemplateContext` from the
final `Snapshot`, and `Sender.SendAsync` dispatches in a goroutine. The
TUI polls `*Status` each tick to render the email row on the Overview
tab.

**Three-layer config merge** (later overrides earlier, per-field):

1. Scenario file `[email]` block
2. `--mail-config <file>` shared TOML
3. Individual `--mail-*` CLI flags

`LOADTEST_SMTP_PASS` env var fills `smtp_pass` when nothing else did.

**Failure handling:** template / validation / SMTP errors are recorded
on the `Status` and surfaced in the TUI rather than crashing the run.
A bad `--mail-config` path warns on stderr and marks every scenario
that wanted email as `Failed` so the user always sees the cause.

---

## `internal/tui/`

```
internal/tui/
├── app.go         ← root Bubble Tea model: messages, modals, keyboard routing
├── types.go       ← Config, ManagedScenario, ScenarioCandidate, MailStatusProvider
├── sidebar.go     ← scenario list (auto-windowed scroll) + suite totals block
├── tabs.go        ← tab bar
├── detail.go      ← right pane: tab dispatcher
├── overview.go    ← Tab 1: scenario header, KPI rows, percentile list, progress, email row
├── latency.go     ← Tab 2: HDR histogram as ASCII bars (uses bucket layout)
├── predicates.go  ← Tab 3: predicate accounting table
├── log.go         ← Tab 4: scrolling log buffer
├── picker.go      ← modal: 'a' add-scenario picker (list of candidates)
├── confirm.go     ← modal: 'x' yes/no confirmation
├── filebrowser.go ← modal: 'b' filesystem .toml picker (wraps bubbles/filepicker)
├── manual.go      ← modal: 'm' manual viewer + pager for `loadtest manual`
├── styles.go      ← Lip Gloss colour and layout constants
└── doc.go
```

**What it is:** The terminal dashboard, built with Bubble Tea
(Elm-style architecture: `Model → Update(msg) → View()`).

**Layout:** Header bar (navy with "Livecharge OCS" in light blue +
"LoadTest" in white). Sidebar listing scenarios with status, sent /
rate / p99 — auto-windowed so the list scrolls when there are too many
scenarios to fit. Bottom of sidebar shows aggregated totals (numbers
in amber-yellow). Right panel: tabs (Overview / Latency / Predicates /
Log). Footer bar with active key hints.

**Modal precedence** (only one open at a time): manual > picker >
confirm > browser.

**Keyboard shortcuts:**

| Key | Action |
| --- | --- |
| `↑` / `↓` | Move scenario selection |
| `1`–`4` | Switch detail tab |
| `Space` | Start scenario (if IDLE) |
| `s` / `r` / `R` | Stop / Resume / Restart |
| `a` | Add scenario (picker over `--config-dirs`) |
| `x` | Remove scenario (with confirm) |
| `b` | Filesystem browser for any `.toml` |
| `m` | Open the embedded operational manual |
| `q` | Quit (drains in-flight emails up to 60 s) |

The TUI never imports the engine's mutators — it reads `Snapshot()` and
`mail.Status` only, via callbacks installed at startup.

---

## `internal/report/`

```
internal/report/
├── csv.go     ← CSVWriter: header row + periodic appended snapshots
└── doc.go
```

**What it is:** Optional CSV export.

**How it works:** When `[report] csv_path` is set, a `CSVWriter` is
started with the scenario. Every `flush_interval` it calls `Snapshot()`
and appends a row. Percentile columns are emitted as **float
milliseconds** (e.g. `0.135`) so sub-millisecond resolution survives
into the CSV. The `{timestamp}` placeholder in `csv_path` is resolved
once at file open so each run produces a uniquely-named file.

---

## `internal/mockserver/`

```
internal/mockserver/
├── server.go       ← MockServer: NATS subscriber or HTTP listener
├── handler.go      ← per-endpoint: extract → pick ok/fail/no-answer → render reply
└── doc.go
```

**What it is:** A minimal test double for OCS-style components.

**Per `[[mock.endpoint]]`:**

1. Subscribe to a NATS subject **or** register an HTTP path
2. On request, optionally extract JSON fields from the body
3. Roll the dice on `fail_rate` (fail) and `no_answer_rate` (silent
   timeout — NATS skips Respond, HTTP blocks until the client gives up)
4. Render the chosen `ok_response` / `fail_response` template and reply

---

## `internal/manual/`

```
internal/manual/
├── embed.go      ← //go:embed manual.md + glamour rendering helpers
└── manual.md     ← the operational manual (this is what `loadtest manual` shows)
```

**What it is:** The operational manual, embedded into the binary at
build time. `loadtest manual` opens it in an interactive viewport (or
prints raw markdown with `--raw`). The TUI's `m` key opens the same
content as an in-dashboard modal.

---

## `scenarios/` and `mock/`

```
scenarios/
├── nats-single-step.toml                       single-step NATS request/reply
├── nats-session.toml                            multi-step NATS with extracts
├── fire-and-forget.toml                         NATS publish, no reply
├── http-basic-auth.toml                         HTTP + Basic auth
├── http-basic-auth-manual-buckets.toml          + custom histogram edges
├── http-basic-auth-manual-buckets-multistep.toml  3-step HTTP session
├── http-basic-auth-with-mail.toml               + post-run email via [email] block
├── http-jwt.toml                                HTTP + JWT Bearer token
└── suite-example.toml                           suite referencing multiple scenarios

mock/
├── nats-mock.toml                               matches the NATS scenarios
├── http-mock.toml                               matches the HTTP scenarios (with fail/no-answer)
└── http-mock-noerrors.toml                      same, but always ok
```

---

## Top-level files

| File | Purpose |
| --- | --- |
| `mail-config.toml` | Example shared `--mail-config` file. **Gitignored** because real ones hold SMTP credentials. |
| `Dockerfile` | Multi-stage build of the `loadtest` binary. |
| `Makefile` | Convenience targets (`make build`, `make test`, `make run`). |
| `go.mod` / `go.sum` | Module is `livecharge/loadtest`. Direct deps: BurntSushi/toml, hdrhistogram-go, charmbracelet/{bubbletea,bubbles,lipgloss,glamour}, nats.go, cobra, mhale/smtpd. |

---

## `docs/`

| File | Contents |
| --- | --- |
| `docs/repository-layout.md` | This file — what is where and why. |
| `docs/superpowers/specs/...` | Design specifications (gitignored). |

The operational manual lives at `internal/manual/manual.md` (embedded
into the binary). It is the user-facing documentation; this file is the
developer-facing map.

---

## How the Packages Connect

```
cmd/loadtest/  (main, run, email, validate, mock, help, manualcmd, version)
  │
  ├── config/          parses TOML → typed structs (incl. *mail.Config)
  │
  ├── engine/          orchestrates sessions, fires OnTerminal
  │    ├── template/   renders templates + manages context + extracts
  │    ├── transport/  sends/receives over NATS or HTTP
  │    └── metrics/    records latency, counters, throughput, log ring
  │
  ├── mail/            renders email body + sends async, depends only on metrics types
  │
  ├── tui/             dashboard (reads metrics.Snapshot + mail.Status only)
  ├── report/          CSV writer (reads metrics.Snapshot only)
  ├── mockserver/      mock NATS / HTTP server (uses template extractor)
  └── manual/          embedded operational manual (used by `loadtest manual` + TUI `m`)
```

Dependency arrows point downward and inward. The TUI never imports the
engine's mutators; it only reads `Snapshot()` and `*mail.Status`. The
engine knows about transports through an interface and fires
`OnTerminal` to a caller-supplied callback — that's how `cmd/loadtest`
plugs in the email feature without the engine importing `internal/mail`.
This keeps every package independently testable and replaceable.
