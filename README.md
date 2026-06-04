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

## License

BSD-3-Clause.
