# Mantle — Milestones

Mantle (github.com/inovacc/logger) is a batteries-included Go application
runtime that wraps any binary — from its Cobra CLI to its core logic — with
PII-redacting structured logging, full OpenTelemetry observability, and
feature-flagged unified config. The runtime flow is cobra (entry) → bootstrap
(wrapper/runtime) → core app.

Part of the inovacc fleet (config, daemon, logger=Mantle). Go 1.25 ·
BSD-3-Clause · Maintainer: Dyam Marcano <dyam.marcano@gmail.com>.

> Note: The module path is still `github.com/inovacc/logger`. The rename to
> `github.com/inovacc/mantle` is a pending follow-up tracked under v0.3.0; the
> **Mantle** brand is adopted in docs first.

---

## v0.1.0 — "Foundation" — [COMPLETE]

End-to-end cobra → wrapper → core works. This milestone establishes the three
core packages and a reference binary that proves the runtime flow.

### Delivered

- **pkg/logger — logging + redaction.** Structured logging on `log/slog`.
  Handler chain (outermost → in): `redactHandler` → `traceHandler` →
  `fanoutHandler` → [JSON/Text local sink, extra `WithSink` sinks]. The
  redaction handler is OUTERMOST so no downstream sink ever sees raw PII.
  Tag-driven PII redaction: `pii:"redact"` (→`[REDACTED]`),
  `pii:"mask"`/`pii:"mask,keep=N"` (reveal last N, default 4),
  `pii:"hash"` (salted sha256, 16 hex), `pii:"-"`/omit/drop; `json:"-"` also
  omitted. `Safe(v)` is a deferred `slog.LogValuer`. `trace_id`/`span_id`
  correlation from the active span. API: `New`, `Init`, `Safe`, `SetHashSalt`,
  `NewRedactor`, `Redactor`, `WithSink`, `WithReplaceAttr`, `WithRedactor`.
  Deps: stdlib + `go.opentelemetry.io/otel/trace` (API ONLY, NO SDK).

- **pkg/obsv — full OpenTelemetry.** `New(ctx, Config, ServiceInfo, ...Option)`
  → `*Stack` with `LogSink()` (an `slog.Handler` for `logger.WithSink`),
  `Tracer(name)`, `Meter(name)`, `Shutdown(ctx)`. Builds logs+traces+metrics
  providers; OTLP gRPC/HTTP + stdout exporters; W3C TraceContext+Baggage
  propagators; resource auto-detect (service.name/version, process, host); Go
  runtime metrics; per-signal gating (`Signals{Logs,Traces,Metrics}`); a FULL
  no-op Stack when `Config.Enabled=false` (never nil, so core code is
  branch-free). Owns the OTel SDK deps: core/sdk v1.32.0, logs/exporters
  v0.8.0, contrib/bridges/otelslog v0.7.0, contrib/instrumentation/runtime
  v0.57.0.

- **pkg/bootstrap — runtime wrapper (cobra → wrapper → core).**
  `Configure[T Configurable](root *cobra.Command, app T, ...Option)` registers
  11 always-present persistent flags + a `PersistentPreRunE` that: loads config
  via a `ConfigSource` (defaults → file → env), overlays CHANGED flags (highest
  precedence), evaluates Features, builds a `Runtime{Cfg,Logger,Tracer,Meter,Shutdown}`,
  and stores it in cmd context. `obsv` is built BEFORE the logger so its
  `LogSink` attaches to the logger fan-out. Composite config: the user
  squash-embeds `bootstrap.Base` (`mapstructure:",squash" yaml:",inline"`).
  Surface: `Base`, `Features{Logging,Observability}`, `Configurable`,
  `DefaultBase()`, `ConfigSource` (viper-backed inovacc/config v1.2.2 default +
  injectable fake for tests), `FromContext` (never nil), `ConfigOf[T]`, `Run`.

- **cmd/logger — reference binary "logger-demo".** Proves cobra → wrapper →
  core: `App` squash-embeds `bootstrap.Base`; `RunE` calls `bootstrap.Run` with
  the core handler.

### Always-present CLI flags

`-c/--config`, `--env`, `--log-level`, `-v/--verbose`, `-q/--quiet`,
`--log-format`, `--log-source`, `--no-redact`, `--otel`, `--otel-endpoint`,
`--otel-protocol`, `--version`. These override file + env values.

### Test Coverage

~85% (logger 81.5%, obsv 86.6%, bootstrap 91.3%, cmd 0%). Target: 80%.

### Release

No git tags yet — greenfield on master, no git remote.

---

## v0.2.0 — "Daemon" — [PLANNED]

Daemon mode (sub-project C, in progress). Build out the gaps in the sibling
`inovacc/daemon` module, then wire it into the runtime.

### Scope

- **Self-spawn guard** via `<APP>_DAEMON_CHILD` env.
- **Self-update** via `ExitUpgrade(4)` binary re-exec (`syscall.Exec` on Unix /
  spawn on Windows).
- **Self-elevate** (UAC on Windows / sudo|polkit on Unix).
- **Persistent service** via kardianos/service:
  install/uninstall/start/stop/status/run.
- **Structured slog lifecycle hooks**: startup, restart+backoff, loop-abort,
  worker-start, signal, shutdown, exit, upgrade.
- **Bootstrap wiring**: `Features.Daemon` / `--daemon` integrated into
  `pkg/bootstrap`.

### Target coverage

80%+.

---

## v0.3.0 — "Release" — [PLANNED]

- **Module rename** `github.com/inovacc/logger` → `github.com/inovacc/mantle`.
- **cmd tests** for the reference binary (currently 0%).
- **CI** pipeline (`go build ./...`, `go vet ./...`, `go test ./... -race`,
  coverage, golangci-lint per fleet convention).
- **First tagged release**.

---

## Architecture invariants

1. The redaction handler is OUTERMOST so no downstream sink ever sees raw PII.
2. Import-graph purity: the `pkg/logger` graph = stdlib + `otel/trace` only; the
   OTel SDK lives ONLY in `pkg/obsv`'s graph; cobra + inovacc/config live ONLY
   in `pkg/bootstrap`. Enforced by `go list -deps` checks.
3. Flags are overlaid via a manual post-load pass (inovacc/config exposes no
   public pflag binding); defaults are applied programmatically via
   `DefaultBase` (the loader ignores `default:` tags).
4. Composite config: the user squash-embeds `bootstrap.Base`
   (`mapstructure:",squash" yaml:",inline"`).
5. `obsv` is built BEFORE the logger so its `LogSink` attaches to the logger
   fan-out; disabled subsystems yield a no-op (never nil) so core code is
   branch-free.

## Known limitations

- Top-level interface-only structs are not auto-redacted unless wrapped with
  `Safe` (slog resolves `LogValuer` only at the top level).
- No regex/content-based PII detection (struct-tag only).
- Redaction hash is a 64-bit salted SHA-256 correlation token
  (brute-forceable for low-entropy inputs); mask reveals length.
- Map keys rendered with `%v` can collide for distinct non-string keys.
- A nested `slog.LogValuer` inside a container is not resolved/redacted.
- inovacc/config is a process-global singleton (one config/process); isolated
  behind `ConfigSource` for tests.
- inovacc/config supports yaml/yml/json only (no toml/dotenv); `default:`
  struct tags are ignored.
- `cmd/logger` writes a default `config.yaml` in cwd when run without
  `--config` (the loader auto-creates it).
- Module-path rename to `github.com/inovacc/mantle` is pending (brand adopted
  in docs first).
