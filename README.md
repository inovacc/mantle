# logger

`github.com/inovacc/logger` — structured logging on `log/slog` with tag-driven
PII redaction and OpenTelemetry trace correlation. Part of the inovacc fleet
(`config`, `daemon`, `logger`).

## Install

```bash
go get github.com/inovacc/logger/pkg/logger
```

## Quick start

```go
import "github.com/inovacc/logger/pkg/logger"

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
    "github.com/inovacc/logger/pkg/logger"
    "github.com/inovacc/logger/pkg/obsv"
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

## License

BSD-3-Clause.
