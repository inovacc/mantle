# Mantle — Features

**Mantle** (`github.com/inovacc/logger`) is a batteries-included Go application runtime that wraps any binary — from its Cobra CLI down to its core logic — with PII-redacting structured logging, full OpenTelemetry observability, and feature-flagged unified config. The runtime flow is **cobra (entry) → bootstrap (wrapper/runtime) → core app**.

> Note: the module-path rename to `github.com/inovacc/mantle` is a pending follow-up. The brand "Mantle" is adopted in docs first; the import path is still `github.com/inovacc/logger`.

Status legend: **Completed** = shipped in milestone v0.1.0 "Foundation" · **Proposed** = planned / backlog.

---

## Completed (v0.1.0 "Foundation")

### Logging — `pkg/logger`
Structured logging built on `log/slog`. Handler chain, outermost → in: `redactHandler → traceHandler → fanoutHandler → [JSON/Text local sink, extra WithSink sinks]`.

- **PII redaction (tag-driven)** — *Completed.* Struct-tag policies:
  - `pii:"redact"` → `[REDACTED]`
  - `pii:"mask"` / `pii:"mask,keep=N"` → reveal last N chars (default 4)
  - `pii:"hash"` → salted SHA-256, 16 hex chars (correlation token)
  - `pii:"-"` / `omit` / `drop`, and `json:"-"` → field omitted
- **`Safe(v)` wrapper** — *Completed.* Deferred `slog.LogValuer` for redacting values (including interface-only structs that slog resolves only at the top level).
- **trace_id / span_id correlation** — *Completed.* `traceHandler` injects IDs from the active OpenTelemetry span.
- **JSON / Text slog logger** — *Completed.* Selectable output format with `AddSource` support.
- **`WithSink` fan-out seam** — *Completed.* `fanoutHandler` lets extra sinks (e.g. the obsv log sink) attach alongside the local sink.
- **Config & API** — *Completed.* `Config{ServiceName, Level, Format, AddSource, Redact(default true), HashSalt, Output}`; public API: `New`, `Init`, `Safe`, `SetHashSalt`, `NewRedactor`, `Redactor`, `WithSink`, `WithReplaceAttr`, `WithRedactor`.
- **Import-graph purity** — *Completed.* Dependencies = stdlib + `go.opentelemetry.io/otel/trace` (API only, **no** SDK). Coverage 81.5%.

### Observability — `pkg/obsv`
Full OpenTelemetry bootstrap. `New(ctx, Config, ServiceInfo, ...Option) → *Stack` exposing `LogSink()`, `Tracer(name)`, `Meter(name)`, `Shutdown(ctx)`.

- **Logs + traces + metrics providers** — *Completed.*
- **Exporters** — *Completed.* OTLP gRPC / HTTP + stdout.
- **`LogSink()` for the logger fan-out** — *Completed.* Returns an `slog.Handler` for `logger.WithSink`.
- **Propagators** — *Completed.* W3C TraceContext + Baggage.
- **Resource auto-detect** — *Completed.* `service.name` / version, process, host.
- **Go runtime metrics** — *Completed.* Via the OTel runtime instrumentation.
- **Per-signal gating** — *Completed.* `Signals{Logs, Traces, Metrics}`.
- **No-op path** — *Completed.* A full no-op `Stack` when `Config.Enabled=false` (never nil), so core code stays branch-free.
- Owns the OTel SDK deps (core/sdk v1.32.0, logs/exporters v0.8.0, `otelslog` v0.7.0, runtime v0.57.0). Coverage 86.6%.

### Runtime / Bootstrap — `pkg/bootstrap`
The cobra → wrapper → core runtime. `Configure[T Configurable](root *cobra.Command, app T, ...Option)`.

- **cobra → wrapper → core runtime** — *Completed.* `PersistentPreRunE` loads config (defaults → file → env), overlays changed flags, evaluates features, builds a `Runtime{Cfg, Logger, Tracer, Meter, Shutdown}`, and stores it in the command context.
- **Composite squash-embedded config** — *Completed.* Users squash-embed `Base` (`Environment`, `Features`, `logger.Config`, `obsv.Config`) via `mapstructure:",squash" yaml:",inline"`.
- **11 always-present flags with manual overlay precedence** — *Completed.* CLI flags override file + env via a manual post-load pass over **changed** flags (highest precedence); defaults applied programmatically via `DefaultBase()` because the loader ignores `default:` tags.
- **Feature-flag gating** — *Completed.* `Features{Logging, Observability}` toggles subsystem construction.
- **Config source seam** — *Completed.* `ConfigSource` interface (viper-backed `inovacc/config` v1.2.2 default + injectable fake for tests).
- **Context & helpers** — *Completed.* `FromContext` (never nil), `ConfigOf[T]`, `Run`. obsv is built **before** the logger so its `LogSink` attaches to the fan-out. Coverage 91.3%.

### Reference binary — `cmd/logger`
- **`logger-demo` reference binary** — *Completed.* Proves cobra → wrapper → core: `App` squash-embeds `bootstrap.Base`; `RunE` calls `bootstrap.Run` with the core handler. No tests yet (0% coverage).

### Always-present CLI flags — *Completed*
Override file + env: `-c/--config`, `--env`, `--log-level`, `-v/--verbose`, `-q/--quiet`, `--log-format`, `--log-source`, `--no-redact`, `--otel`, `--otel-endpoint`, `--otel-protocol`, `--version`.

---

## Proposed

- **Daemon mode** — *Proposed* (milestone v0.2.0 "Daemon", in progress). Build out the sibling `inovacc/daemon` module: self-spawn guard (`<APP>_DAEMON_CHILD` env), self-update via `ExitUpgrade(4)` binary re-exec (`syscall.Exec` on Unix / spawn on Windows), self-elevate (UAC / sudo|polkit), persistent `kardianos/service` install (install/uninstall/start/stop/status/run), structured slog lifecycle hooks (startup, restart+backoff, loop-abort, worker-start, signal, shutdown, exit, upgrade); then wire `Features.Daemon` / `--daemon` into `pkg/bootstrap`.
- **Regex / content-based PII detection** — *Proposed.* Today redaction is struct-tag only; add pattern-based detection beyond tags.
- **Config hot-reload (`WatchConfig`)** — *Proposed.* Live config reload on file change.
- **Secret encrypt-at-rest** — *Proposed.* Encrypt sensitive config values on disk.
- **Metrics / trace helpers** — *Proposed.* Ergonomic helpers over `Tracer` / `Meter` for common spans and counters.
- **OTLP auth header presets** — *Proposed.* Ready-made auth-header configurations for common OTLP backends.

---

## Known limitations (current, by design)

- Top-level interface-only structs are not auto-redacted unless wrapped with `Safe` (slog resolves `LogValuer` only at the top level).
- No regex/content-based PII detection — struct-tag only.
- Redaction hash is a 64-bit salted SHA-256 correlation token (brute-forceable for low-entropy inputs); mask reveals length.
- Map keys rendered with `%v` can collide for distinct non-string keys.
- A nested `slog.LogValuer` inside a container is not resolved/redacted.
- `inovacc/config` is a process-global singleton (one config per process); isolated behind `ConfigSource` for tests.
- `inovacc/config` supports yaml/yml/json only (no toml/dotenv); `default:` struct tags are ignored.
- `cmd/logger` writes a default `config.yaml` in the cwd when run without `--config` (loader auto-creates).
- Module-path rename to `github.com/inovacc/mantle` is pending (brand adopted in docs first).
