# ADR 0004 — Mantle: Isolate the OpenTelemetry SDK in pkg/obsv

- Status: Accepted
- Date: 2026-06-04
- Maintainer: Dyam Marcano <dyam.marcano@gmail.com>
- Milestone: v0.1.0 "Foundation"
- Project: Mantle (github.com/inovacc/mantle)

## Context

Mantle is a batteries-included Go application runtime that wraps any binary —
from its Cobra CLI to its core logic — with PII-redacting structured logging,
full OpenTelemetry observability, and feature-flagged unified config. The flow
is `cobra (entry) -> bootstrap (wrapper/runtime) -> core app`.

`pkg/logger` is the lowest, most widely depended-on layer. It must be importable
anywhere — including from libraries and tools that have no interest in
observability, config loading, or a CLI framework — with minimal dependency
weight. Pulling the full OpenTelemetry SDK, its OTLP exporters, and the contrib
modules into every consumer of the logger would inflate build graphs, slow
builds, and couple a simple structured-logging package to a heavy, fast-moving
telemetry stack.

At the same time Mantle needs first-class OpenTelemetry: logs, traces, and
metrics providers; OTLP gRPC/HTTP and stdout exporters; W3C TraceContext +
Baggage propagation; resource auto-detection; and Go runtime metrics. That
machinery has to live *somewhere*, and it has to feed log records into the same
slog pipeline as the local sink.

The tension: the logger wants a thin import graph, but observability needs the
heavy SDK and needs to reach the logger.

## Decision

Keep three packages on strictly separated import graphs, enforced in CI.

1. **`pkg/logger` — stdlib + `go.opentelemetry.io/otel/trace` (API only).**
   The logger imports the OpenTelemetry *trace API* solely to read `trace_id` /
   `span_id` off the active span for log correlation. It never imports the OTel
   SDK, exporters, or contrib bridges. Its graph is `stdlib + otel/trace`.

2. **`pkg/obsv` — owns the entire OTel SDK surface.**
   The full SDK, OTLP/stdout exporters, and contrib modules live *only* in
   `pkg/obsv`. `New(ctx, Config, ServiceInfo, ...Option) -> *Stack` builds the
   logs/traces/metrics providers, propagators, resource, and runtime metrics.
   When `Config.Enabled=false` it returns a full no-op `Stack` (never nil), so
   core code stays branch-free.

3. **The bridge is a `slog.Handler` seam, not an import.**
   `obsv` exposes its OTel log bridge through `Stack.LogSink()`, which returns a
   `slog.Handler`. The logger consumes it via `logger.WithSink` and places it in
   its fan-out. Data flows `obsv -> logger` at runtime through a stdlib
   interface; `logger` never imports `obsv` and never imports the SDK. `obsv` is
   built *before* the logger precisely so its `LogSink()` can be attached to the
   fan-out at construction time.

4. **`cobra` + `inovacc/config` live only in `pkg/bootstrap`.**
   The CLI framework and the config loader are confined to the wrapper layer, so
   neither `logger` nor `obsv` inherits a CLI or a config singleton.

This preserves the architecture invariant of import-graph purity:
`pkg/logger` = stdlib + `otel/trace`; the OTel SDK lives only in `pkg/obsv`;
`cobra` + `inovacc/config` live only in `pkg/bootstrap`.

## Pinned version matrix (owned by pkg/obsv)

| Component | Version |
|-----------|---------|
| OTel core / SDK | v1.32.0 |
| logs SDK + exporters | v0.8.0 |
| contrib bridge `otelslog` | v0.7.0 |
| contrib `instrumentation/runtime` | v0.57.0 |

Only `go.opentelemetry.io/otel/trace` (the stable API) crosses into
`pkg/logger`. The `v0.x` log/contrib modules are still pre-stable and may churn;
isolating them in `obsv` means that churn never reaches the logger's consumers.

## Consequences

### Positive
- `pkg/logger` stays light and reusable — importable from any library or tool
  without dragging in the SDK, exporters, or a CLI.
- The boundary is mechanically verifiable: `go list -deps` checks in CI assert
  that the SDK never appears in `pkg/logger`'s graph and that `cobra` /
  `inovacc/config` never leak out of `pkg/bootstrap`.
- Disabled observability is a no-op `Stack` (never nil), keeping core code
  branch-free regardless of whether the SDK is active.
- Fast-moving `v0.x` log/contrib dependencies are quarantined in one package,
  shrinking the blast radius of a telemetry-stack upgrade.

### Negative / trade-offs
- `obsv` cannot enrich the logger by importing it; it must bridge in through the
  `WithSink` seam. The contract between the two is the `slog.Handler` returned by
  `LogSink()`, so changes to that seam must stay backward compatible.
- Ordering matters: `obsv` must be constructed before the logger so its
  `LogSink()` can join the fan-out. Bootstrap encodes this ordering explicitly.
- The logger's correlation feature still depends on `otel/trace`; should that
  API ever break, the logger is affected. This is accepted because `otel/trace`
  is the stable, slow-moving part of the OpenTelemetry Go surface.

## Enforcement

CI runs `go list -deps ./pkg/logger/...` and fails if any
`go.opentelemetry.io/otel/sdk`, exporter, or contrib path appears. Equivalent
checks assert the SDK is present only under `pkg/obsv` and that `cobra` /
`inovacc/config` appear only under `pkg/bootstrap`.

## Related

- ADR 0001–0003 (foundation decisions for logging, redaction, and config layering)
- `pkg/logger` — handler chain: redact -> trace -> fanout -> [local sink + WithSink sinks]
- `pkg/obsv` — OTel bootstrap; `Stack.LogSink()`
- `pkg/bootstrap` — cobra -> wrapper -> core runtime
