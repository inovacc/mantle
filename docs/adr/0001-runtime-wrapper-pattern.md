# ADR 0001 — Mantle Runtime Wrapper Pattern (cobra → bootstrap → core)

- **Status:** Accepted
- **Date:** 2026-06-04
- **Maintainer:** Dyam Marcano <dyam.marcano@gmail.com>
- **Scope:** Mantle (github.com/inovacc/logger), milestone v0.1.0 "Foundation"

## Context

The inovacc fleet (config, daemon, logger=Mantle) wants its binaries to be
batteries-included: PII-redacting structured logging, full OpenTelemetry
observability, and feature-flagged unified config — without each binary
re-wiring slog handlers, OTel providers, config loading, and flag precedence by
hand. Re-implementing that plumbing per command is repetitive, error-prone, and
drifts over time (one binary forgets to attach the redaction handler outermost,
another wires the logger before observability so the OTLP log sink never joins
the fan-out).

Mantle already provides the building blocks as import-pure packages:

- `pkg/logger` — slog-based structured logging with tag-driven PII redaction
  (`pii:"redact"|"mask"|"hash"|"-"`), `trace_id`/`span_id` correlation, and a
  handler chain `redactHandler → traceHandler → fanoutHandler → sinks`. Graph is
  stdlib + `otel/trace` (API only).
- `pkg/obsv` — full OpenTelemetry bootstrap producing a `*Stack` with
  `LogSink()`, `Tracer`, `Meter`, and `Shutdown`; a complete no-op (never nil)
  Stack when disabled. Owns the OTel SDK deps.
- `pkg/bootstrap` — the glue that turns those blocks into one runtime.

What was missing was a single, opinionated assembly point that composes these in
the correct order and hands the core application a ready-to-use runtime, while
keeping the import graphs pure (the OTel SDK confined to `pkg/obsv`; cobra and
`inovacc/config` confined to `pkg/bootstrap`).

## Decision

Adopt a **cobra → wrapper(bootstrap) → core-app** runtime pattern, exposed
through a single call:

```go
bootstrap.Configure[T Configurable](root *cobra.Command, app T, ...Option)
```

`Configure`:

1. **Registers 11 always-present persistent flags** on the root command that
   override file and env values:
   `-c/--config`, `--env`, `--log-level`, `-v/--verbose`, `-q/--quiet`,
   `--log-format`, `--log-source`, `--no-redact`, `--otel`, `--otel-endpoint`,
   `--otel-protocol`, `--version`.
2. **Installs a `PersistentPreRunE`** that, in order:
   - loads config through a `ConfigSource` (defaults → file → env), where the
     default source is viper-backed `inovacc/config` v1.2.2 and a fake source is
     injectable for tests;
   - applies programmatic defaults via `DefaultBase()` (the loader ignores
     `default:` struct tags, so defaults are set in code, not tags);
   - overlays only the **CHANGED** flags as the highest-precedence layer;
   - evaluates `Features{Logging, Observability}`;
   - builds observability **before** the logger so `obsv.LogSink()` attaches to
     the logger fan-out;
   - assembles a `Runtime{Cfg, Logger, Tracer, Meter, Shutdown}` and stores it in
     the command's context.

The core application is a decoupled handler — `func(ctx, *Runtime) error` — that
retrieves the runtime via `FromContext` (never nil) and calls `bootstrap.Run`.
Users compose their config by squash-embedding `bootstrap.Base`
(`mapstructure:",squash" yaml:",inline"`), which carries `Environment`,
`Features`, `logger.Config`, and `obsv.Config`. The unexported `base()` method on
`Configurable` means only types embedding `Base` satisfy the interface.

The reference binary `cmd/logger` ("logger-demo") proves the full
cobra → wrapper → core path end to end.

## Consequences

**Positive**

- **One call wires everything.** A new fleet binary gets logging, observability,
  and config — with correct ordering and precedence — from a single
  `Configure` call. No per-binary re-wiring.
- **The core app stays decoupled.** Business logic is a plain
  `func(ctx, *Runtime) error`; it never imports cobra, the OTel SDK, or the
  config loader, and reads everything from an injected `Runtime`.
- **Branch-free core.** Disabled subsystems yield no-op (never nil) Tracer,
  Meter, and Shutdown, so core code needs no `if logging enabled` guards.
- **Safety-by-construction.** Redaction stays outermost in the handler chain and
  observability is built before the logger, so no downstream sink ever sees raw
  PII and the OTLP log sink always joins the fan-out.
- **Import-graph purity is preserved.** The OTel SDK lives only in `pkg/obsv`;
  cobra and `inovacc/config` live only in `pkg/bootstrap`; `pkg/logger` stays
  stdlib + `otel/trace`. Enforced via `go list -deps` checks.
- **Testable.** `ConfigSource` isolates the process-global `inovacc/config`
  singleton behind an interface, allowing a fake source in tests. Coverage:
  `pkg/bootstrap` 91.3%, `pkg/obsv` 86.6%, `pkg/logger` 81.5%.

**Negative / trade-offs**

- **Relies on cobra.** The pattern is coupled to cobra's command and
  `PersistentPreRunE` lifecycle; a non-cobra entry point cannot reuse
  `Configure` as-is.
- **Manual flag overlay.** `inovacc/config` exposes no public pflag binding, so
  flag precedence is implemented as a manual post-load pass over CHANGED flags,
  and defaults are applied programmatically through `DefaultBase()` rather than
  via `default:` tags.
- **Single config per process.** `inovacc/config` is a process-global singleton
  (one config per process), accepted because each binary loads exactly one
  config; tests work around it through the injectable `ConfigSource`.
- **Demo side effect.** Run without `--config`, `cmd/logger` writes a default
  `config.yaml` into the current working directory (the loader auto-creates it).

## Follow-ups

- The Go module path is still `github.com/inovacc/logger`; the rename to
  `github.com/inovacc/mantle` is a pending follow-up (the Mantle brand is being
  adopted in docs first).
- Milestone v0.2.0 "Daemon" will wire `Features.Daemon` / `--daemon` into
  `pkg/bootstrap`, layering daemon lifecycle (self-spawn guard, self-update via
  `ExitUpgrade(4)` re-exec, self-elevate, and `kardianos/service` install) on top
  of this runtime wrapper.
