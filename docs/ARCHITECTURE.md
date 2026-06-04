# Mantle Architecture

Mantle (`github.com/inovacc/mantle`) is a batteries-included Go application runtime
that wraps any binary — from its Cobra CLI down to its core logic — with
PII-redacting structured logging, full OpenTelemetry observability, and
feature-flagged unified config. The flow is always **cobra (entry) → bootstrap
(wrapper/runtime) → core app**.

> Module-path rename to `github.com/inovacc/mantle` is a pending follow-up; the
> brand "Mantle" is adopted in docs first while the import path stays
> `github.com/inovacc/mantle`.

The runtime is composed of three layered packages plus a reference binary:

| Package | Role | Dependency boundary |
|---------|------|---------------------|
| `pkg/logger` | Structured logging on `log/slog` with tag-driven PII redaction and trace correlation | stdlib + `otel/trace` (API only, **no SDK**) |
| `pkg/obsv` | Full OpenTelemetry bootstrap (logs + traces + metrics) | owns the OTel **SDK** deps |
| `pkg/bootstrap` | The cobra → wrapper → core runtime, config load, flag overlay, feature gating | owns `cobra` + `inovacc/config` |
| `cmd/logger` | Reference binary `logger-demo` proving the end-to-end wiring | composes the above |

---

## System Overview

This flowchart shows how the layers connect at startup. The CLI layer parses
flags, the wrapper layer (`bootstrap.Configure`) loads config and builds the
`Runtime`, the subsystems provide logging and observability, and the core app
receives a single `func(ctx, *Runtime) error` with everything it needs.

```mermaid
flowchart TB
    subgraph CLI["CLI layer — cobra"]
        ROOT["root cmd (logger-demo)"]
        FLAGS["11 always-present persistent flags<br/>-c/--config, --env, --log-level,<br/>-v/--verbose, -q/--quiet, --log-format,<br/>--log-source, --no-redact, --otel,<br/>--otel-endpoint, --otel-protocol, --version"]
        LEAF["leaf RunE"]
    end

    subgraph WRAP["Wrapper layer — pkg/bootstrap"]
        CONF["Configure[T Configurable]"]
        PRE["PersistentPreRunE"]
        LOAD["ConfigSource.Load<br/>(defaults → file → env)"]
        OVERLAY["overlay CHANGED flags<br/>(highest precedence)"]
        FEAT["evaluate Features<br/>{Logging, Observability}"]
        BUILD["buildRuntime → Runtime<br/>{Cfg, Logger, Tracer, Meter, Shutdown}"]
    end

    subgraph SUBS["Subsystems"]
        LOGGER["pkg/logger<br/>handler chain"]
        OBSV["pkg/obsv<br/>Stack (OTel SDK)"]
        CONFIG["inovacc/config<br/>(viper-backed)"]
    end

    CORE["Core app<br/>func(ctx, *Runtime) error"]

    ROOT --> FLAGS
    CONF -->|registers flags + PersistentPreRunE| ROOT
    ROOT -->|invokes| PRE
    PRE --> LOAD
    LOAD -->|reads via| CONFIG
    LOAD --> OVERLAY
    FLAGS -.->|CHANGED values| OVERLAY
    OVERLAY --> FEAT
    FEAT --> BUILD
    BUILD -->|obsv.New first| OBSV
    BUILD -->|logger.Init w/ WithSink| LOGGER
    OBSV -.->|LogSink fans into| LOGGER
    BUILD -->|stored in cmd context| LEAF
    LEAF -->|bootstrap.Run| CORE
    CORE -->|uses| LOGGER
    CORE -->|uses| OBSV
```

---

## Request/Boot Flow

The boot sequence is driven by Cobra's `PersistentPreRunE`, which runs before any
leaf command. Config is resolved with strict precedence (programmatic defaults →
file → env → changed flags), then observability is built **before** the logger so
its `LogSink()` can attach to the logger's fan-out. The assembled `Runtime` is
stored in the command context and retrieved by the leaf via `bootstrap.Run`.

```mermaid
sequenceDiagram
    actor User
    participant Cobra as cobra root
    participant Pre as PersistentPreRunE
    participant Src as ConfigSource
    participant Build as buildRuntime
    participant Obsv as obsv.New
    participant Log as logger.Init
    participant Ctx as cmd context
    participant Leaf as leaf RunE
    participant Core as core app

    User->>Cobra: run logger-demo [flags]
    Cobra->>Pre: PersistentPreRunE
    Pre->>Src: Load() (defaults → file → env)
    Src-->>Pre: composite Base config
    Pre->>Pre: overlay CHANGED flags (highest precedence)
    Pre->>Pre: evaluate Features {Logging, Observability}
    Pre->>Build: buildRuntime(ctx, Base, options)
    Build->>Obsv: obsv.New(ctx, Config, ServiceInfo)
    Obsv-->>Build: Stack (LogSink, Tracer, Meter, Shutdown)
    Build->>Log: logger.Init(Config, WithSink(stack.LogSink()))
    Log-->>Build: *slog.Logger
    Build-->>Pre: Runtime {Cfg, Logger, Tracer, Meter, Shutdown}
    Pre->>Ctx: store Runtime
    Cobra->>Leaf: RunE
    Leaf->>Core: bootstrap.Run → core(ctx, FromContext(ctx))
    Core-->>Leaf: error
    Leaf->>Build: rt.Shutdown(ctx) (flush observability, reverse order)
```

---

## Logging Handler Chain

A log record passes through a fixed handler chain. **Redaction is the outermost
handler**, so no downstream sink — neither the local JSON/Text sink nor the OTel
bridge — ever observes raw PII. Trace correlation is injected next, then the
record fans out to all configured sinks.

```mermaid
flowchart LR
    REC["slog.Record<br/>(may carry pii-tagged structs / Safe(v))"]
    RED["redactHandler<br/>OUTERMOST — scrub PII<br/>redact / mask / hash / drop"]
    TRC["traceHandler<br/>inject trace_id + span_id<br/>from active span"]
    FAN["fanoutHandler<br/>clone + dispatch"]
    JSON["local sink<br/>JSON / Text"]
    BRIDGE["OTel bridge<br/>(obsv LogSink via WithSink)"]

    REC --> RED
    RED --> TRC
    TRC --> FAN
    FAN --> JSON
    FAN --> BRIDGE

    note["Redaction outermost: no sink sees raw PII.<br/>Tags: pii:redact → [REDACTED], pii:mask/mask,keep=N → reveal last N (default 4),<br/>pii:hash → salted sha256 (16 hex), pii:- / omit / drop and json:- → omitted."]
    RED -.-> note
```

The redactor resolves any `slog.LogValuer` (including `Safe(v)`) first, then walks
struct fields applying `pii` tags. Known limitation: top-level interface-only
structs are not auto-redacted unless wrapped with `Safe`, and a nested
`slog.LogValuer` inside a container is not resolved/redacted.

---

## Observability Stack

`obsv.New` builds a full OpenTelemetry `Stack` from a resource and per-signal
exporters. Each signal (logs, traces, metrics) is independently gated, exporters
are selectable (OTLP gRPC/HTTP or stdout), and the resulting providers are
installed as OTel globals alongside W3C TraceContext + Baggage propagators. The
`LogSink()` handler bridges OTel logs back into the Mantle logger's fan-out.

```mermaid
flowchart TB
    NEW["obsv.New(ctx, Config, ServiceInfo, ...Option)"]
    RES["resource auto-detect<br/>service.name/version, process, host"]
    GATE["per-signal gating<br/>Signals{Logs, Traces, Metrics}"]

    subgraph EXP["Exporters (per signal)"]
        OTLPG["OTLP gRPC"]
        OTLPH["OTLP HTTP"]
        STDOUT["stdout"]
    end

    subgraph PROV["Providers"]
        LP["LoggerProvider"]
        TP["TracerProvider"]
        MP["MeterProvider<br/>(+ Go runtime metrics)"]
    end

    GLOB["OTel globals + propagators<br/>W3C TraceContext + Baggage"]
    SINK["LogSink() → slog.Handler"]
    LOGGER["pkg/logger fan-out<br/>(via logger.WithSink)"]
    STACK["Stack{ LogSink, Tracer(name), Meter(name), Shutdown(ctx) }"]

    NEW --> RES
    NEW --> GATE
    RES --> PROV
    GATE -->|enabled signals| EXP
    EXP --> PROV
    PROV --> GLOB
    LP --> SINK
    SINK -->|bridges into| LOGGER
    PROV --> STACK
    GLOB --> STACK
```

---

## Feature Gating

Subsystems are toggled by `Features{Logging, Observability}` resolved during boot.
A **disabled subsystem yields a fully no-op implementation, never `nil`** — `obsv`
returns a no-op `Stack` (no-op `Tracer`/`Meter` and a `Shutdown` that does
nothing), and `buildRuntime` falls back to no-op tracer/meter and a discard
logger. This keeps core application code branch-free: it can always call
`rt.Logger`, `rt.Tracer`, `rt.Meter`, and `rt.Shutdown` without nil checks.
Likewise, `FromContext` always returns a usable `Runtime` even when none was
stored.

```mermaid
flowchart LR
    F["Features evaluated"]
    F -->|Observability = true| OON["obsv.New → live Stack<br/>(real providers + LogSink)"]
    F -->|Observability = false| OOFF["no-op Stack<br/>(no-op Tracer/Meter, Shutdown=nil-op)"]
    F -->|Logging = true| LON["logger.Init → *slog.Logger<br/>(redact → trace → fanout)"]
    F -->|Logging = false| LOFF["discard logger<br/>(slog.TextHandler → io.Discard)"]

    OON --> RT["Runtime (never nil fields)"]
    OOFF --> RT
    LON --> RT
    LOFF --> RT
    RT -->|branch-free| CORE["core app: rt.Logger / rt.Tracer /<br/>rt.Meter / rt.Shutdown always callable"]
```

### Architecture invariants

1. **Redaction is outermost** so no downstream sink ever sees raw PII.
2. **Import-graph purity** — `pkg/logger` = stdlib + `otel/trace` only; the OTel
   SDK lives only in `pkg/obsv`; `cobra` + `inovacc/config` live only in
   `pkg/bootstrap`. Enforced by `go list -deps` checks.
3. Flags are overlaid via a manual post-load pass (inovacc/config exposes no
   public pflag binding); defaults are applied programmatically via `DefaultBase`
   because the loader ignores `default:` tags.
4. **Composite config** — users squash-embed `bootstrap.Base`
   (`mapstructure:",squash" yaml:",inline"`).
5. `obsv` is built **before** the logger so its `LogSink` attaches to the logger
   fan-out; disabled subsystems yield no-op (never nil) so core code is
   branch-free.
