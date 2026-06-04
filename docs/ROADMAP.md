# Mantle — Roadmap

Mantle (github.com/inovacc/mantle) is a batteries-included Go application runtime
that wraps any binary — from its Cobra CLI to its core logic — with PII-redacting
structured logging, full OpenTelemetry observability, and feature-flagged unified
config. Flow: cobra (entry) → bootstrap (wrapper/runtime) → core app.

**Overall progress: ~75%** — milestone v0.1.0 "Foundation" complete; v0.2.0
"Daemon" in progress.

> Note: the Go module path is still `github.com/inovacc/mantle`. The "Mantle" brand
> is adopted in docs first; the module-path rename to
> `github.com/inovacc/mantle` is a pending follow-up (tracked in Phase 5).

---

## Phase 1 — Structured logging + PII redaction `[COMPLETE]`

Package: `pkg/logger` — structured logging on `log/slog`.

- Handler chain (outermost → in): `redactHandler` → `traceHandler` →
  `fanoutHandler` → [JSON/Text local sink, extra `WithSink` sinks].
- Tag-driven PII redaction: `pii:"redact"` (→`[REDACTED]`),
  `pii:"mask"` / `pii:"mask,keep=N"` (reveal last N, default 4),
  `pii:"hash"` (salted SHA-256, 16 hex), `pii:"-"`/omit/drop;
  `json:"-"` also omitted.
- `Safe(v)` deferred `slog.LogValuer`; `trace_id`/`span_id` correlation from the
  active span.
- `Config{ServiceName, Level, Format, AddSource, Redact (default true), HashSalt, Output}`.
- API: `New`, `Init`, `Safe`, `SetHashSalt`, `NewRedactor`, `Redactor`,
  `WithSink`, `WithReplaceAttr`, `WithRedactor`.
- Invariant: redaction handler is **outermost** so no downstream sink ever sees
  raw PII.
- Import-graph purity: stdlib + `go.opentelemetry.io/otel/trace` (API only, no SDK).

Coverage: **81.5%**.

## Phase 2 — Full OpenTelemetry observability `[COMPLETE]`

Package: `pkg/obsv` — full OpenTelemetry bootstrap.

- `New(ctx, Config, ServiceInfo, ...Option) → *Stack` with `LogSink()`
  (an `slog.Handler` for `logger.WithSink`), `Tracer(name)`, `Meter(name)`,
  `Shutdown(ctx)`.
- Builds logs + traces + metrics providers; OTLP gRPC/HTTP + stdout exporters;
  W3C TraceContext + Baggage propagators; resource auto-detect
  (service.name/version, process, host); Go runtime metrics.
- Per-signal gating via `Signals{Logs, Traces, Metrics}`; **full no-op Stack**
  when `Config.Enabled=false` (never nil, so core code stays branch-free).
- Owns the OTel SDK deps: core/sdk v1.32.0, logs/exporters v0.8.0,
  contrib/bridges/otelslog v0.7.0, contrib/instrumentation/runtime v0.57.0.
- Invariant: obsv is built **before** the logger so its `LogSink` attaches to the
  logger fan-out.

Coverage: **86.6%**.

## Phase 3 — cobra → wrapper → core runtime + unified config `[COMPLETE]`

Package: `pkg/bootstrap` — the cobra → wrapper → core runtime.

- `Configure[T Configurable](root *cobra.Command, app T, ...Option)` registers
  11 always-present persistent flags + a `PersistentPreRunE` that loads config via
  a `ConfigSource` (defaults → file → env), overlays CHANGED flags (highest
  precedence), evaluates Features, builds a `Runtime{Cfg, Logger, Tracer, Meter,
  Shutdown}`, and stores it in the command context.
- `Base` (squash-embeddable: `Environment`, `Features`, `logger.Config`,
  `obsv.Config`), `Features{Logging, Observability}`, `Configurable` (unexported
  `base()` — only embedding `Base` satisfies it), `DefaultBase()` (programmatic
  defaults; the loader ignores `default:` tags), `ConfigSource` interface
  (viper-backed inovacc/config v1.2.2 default + injectable fake for tests),
  `FromContext` (never nil), `ConfigOf[T]`, `Run`.
- Always-present CLI flags (override file + env): `-c/--config`, `--env`,
  `--log-level`, `-v/--verbose`, `-q/--quiet`, `--log-format`, `--log-source`,
  `--no-redact`, `--otel`, `--otel-endpoint`, `--otel-protocol`, `--version`.
- Composite config: users squash-embed `bootstrap.Base`
  (`mapstructure:",squash" yaml:",inline"`).
- Invariant: flags overlaid via a manual post-load pass (inovacc/config exposes no
  public pflag binding); cobra + inovacc/config live only in this graph.

Reference binary: `cmd/logger` ("logger-demo") proves cobra → wrapper → core;
`App` squash-embeds `bootstrap.Base` and `RunE` calls `bootstrap.Run` with the
core handler. No tests yet (0%).

Coverage: **91.3%**.

## Phase 4 — Daemon mode `[IN PROGRESS]`

Sub-project C — milestone v0.2.0 "Daemon". Build out the sibling
`inovacc/daemon` module's gaps, then wire `Features.Daemon` / `--daemon` into
`pkg/bootstrap`.

- Self-spawn guard via `<APP>_DAEMON_CHILD` env.
- Self-update via `ExitUpgrade(4)` binary re-exec (`syscall.Exec` on Unix /
  spawn on Windows).
- Self-elevate (UAC on Windows / sudo|polkit on Unix).
- Persistent `kardianos/service` install: install / uninstall / start / stop /
  status / run.
- Structured slog lifecycle hooks: startup, restart + backoff, loop-abort,
  worker-start, signal, shutdown, exit, upgrade.
- Wire `Features.Daemon` / `--daemon` into `pkg/bootstrap`.

## Phase 5 — Hardening & release `[PLANNED]`

Milestone hardening before a tagged release.

- Tests for `cmd/logger` (currently 0%).
- Module-path rename `github.com/inovacc/mantle` → `github.com/inovacc/mantle`.
- Regex / content-based PII detection (today: struct-tag only).
- Config zero-dep adapter (decouple from the inovacc/config process-global
  singleton; add formats beyond yaml/yml/json).

---

## Test Coverage

Current **~85%** (tested packages) | Target **80%**.

| Package          | Coverage | Status     |
|------------------|----------|------------|
| `pkg/logger`     | 81.5%    | Good       |
| `pkg/obsv`       | 86.6%    | Good       |
| `pkg/bootstrap`  | 91.3%    | Good       |
| `cmd/logger`     | 0.0%     | No tests   |
