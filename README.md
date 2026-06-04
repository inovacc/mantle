# Mantle

> **Mantle** is the brand name; the Go import path is
> `github.com/inovacc/mantle` (the module-path rename from `.../logger` is done).

A batteries-included Go application runtime that wraps any binary — from its
Cobra CLI down to its core logic — with **PII-redacting structured logging**,
**full OpenTelemetry observability**, and **feature-flagged unified config**. The
flow is `cobra → Mantle (wrapper) → your core app`. Part of the inovacc fleet
(`config`, `daemon`, `logger`/Mantle).

**Packages:** `pkg/logger` (slog + PII redaction + trace correlation) ·
`pkg/obsv` (full OTel: logs + traces + metrics) · `pkg/bootstrap` (the
`cobra→wrapper→core` runtime) · `cmd/logger` (reference binary).

## Install

```bash
go get github.com/inovacc/mantle/pkg/logger
```

## Quick start

```go
import "github.com/inovacc/mantle/pkg/logger"

lg, err := logger.New(logger.Config{
    ServiceName: "checkout-api",
    Level:       "info",   // debug | info | warn | error
    Format:      "json",   // json | text
    Redact:      true,     // tag-driven PII redaction (on by default)
    HashSalt:    "rotate-me",
})
if err != nil { panic(err) }

lg.InfoContext(ctx, "user signed up", slog.Any("user", u))
```

## PII redaction

Tag struct fields with `pii:"..."`:

| Tag | Effect |
|-----|--------|
| `pii:"redact"` / `pii:"true"` | replace with `[REDACTED]` |
| `pii:"mask"` / `pii:"mask,2"` / `pii:"mask,keep=6"` | reveal last N chars (default 4) |
| `pii:"hash"` | salted SHA-256 digest (`sha256:` + 16 hex) |
| `pii:"-"` / `omit` / `drop` | drop the field |
| `json:"-"` | also dropped from logs |

Opt in per value without global redaction:

```go
lg.Info("signup", slog.Any("user", logger.Safe(u)))
```

## Observability

`pkg/logger` depends only on the stdlib and the OTel **trace API** (for
`trace_id`/`span_id` correlation). Full OpenTelemetry export (logs, traces,
metrics) is provided by `pkg/obsv`, which attaches via:

```go
lg, _ := logger.New(cfg, logger.WithSink(otelBridgeHandler))
```

### Full OpenTelemetry (pkg/obsv)

`pkg/obsv` bootstraps logs + traces + metrics in one call and bridges logs back
into the logger:

```go
import (
    "github.com/inovacc/mantle/pkg/logger"
    "github.com/inovacc/mantle/pkg/obsv"
)

stack, _ := obsv.New(ctx, obsv.Config{Enabled: true, Endpoint: "localhost:4317", Insecure: true},
    obsv.ServiceInfo{Name: "checkout", Version: "1.2.0", Environment: "prod"})
defer stack.Shutdown(ctx)

lg, _ := logger.New(logger.Config{ServiceName: "checkout", Redact: true},
    logger.WithSink(stack.LogSink()))

ctx, span := stack.Tracer("checkout").Start(ctx, "PlaceOrder")
defer span.End()
```

Disabled (`Enabled:false`) yields a no-op stack — `LogSink()` is nil (logger skips
it), `Tracer`/`Meter` are no-ops, `Shutdown` does nothing.

## Application runtime (pkg/bootstrap)

Wire one wrapper from your Cobra CLI to your core app — config, logging, and
observability included:

```go
type App struct {
    bootstrap.Base `mapstructure:",squash" yaml:",inline"`
    Greeting       string `mapstructure:"greeting"`
}

app := &App{Base: bootstrap.DefaultBase(), Greeting: "hello"}
root := &cobra.Command{Use: "myapp", RunE: func(cmd *cobra.Command, _ []string) error {
    return bootstrap.Run(cmd, func(ctx context.Context, rt *bootstrap.Runtime) error {
        rt.Logger.InfoContext(ctx, "running", slog.String("greeting", bootstrap.ConfigOf[*App](rt).Greeting))
        return rt.Shutdown(ctx)
    })
}}
bootstrap.Configure(root, app, bootstrap.WithAppName("myapp"), bootstrap.WithVersion("1.0.0"))
root.Execute()
```

Always-present flags (highest precedence over file+env): `-c/--config`, `--env`,
`--log-level`, `-v/-q`, `--log-format`, `--log-source`, `--no-redact`, `--otel`,
`--otel-endpoint`, `--otel-protocol`, `--version`. Enable subsystems via the
`features:` config block or flags.

## Documentation

See [`docs/`](docs/): [ARCHITECTURE](docs/ARCHITECTURE.md) ·
[ROADMAP](docs/ROADMAP.md) · [MILESTONES](docs/MILESTONES.md) ·
[BACKLOG](docs/BACKLOG.md) · [FEATURES](docs/FEATURES.md) ·
[ISSUES](docs/ISSUES.md) · [CLI verbs](docs/VERBS.md) ·
[ADRs](docs/adr/) · [BRANDING](docs/BRANDING.md) ·
[CONTRIBUTORS](docs/CONTRIBUTORS.md).

## License

BSD-3-Clause.
