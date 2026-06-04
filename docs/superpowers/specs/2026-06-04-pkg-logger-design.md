# Sub-project A — `pkg/logger` Design Spec

- **Date:** 2026-06-04
- **Status:** Approved (design); pending user spec review
- **Parent:** `2026-06-04-logger-runtime-architecture-design.md`
- **Package:** `github.com/inovacc/mantle/pkg/logger`
- **Nature:** **In-place refactor** of the seed already present in `D:\weaver-sync\modules\logger`
  (`logger.go`, `handler.go`, `redact.go`, `otel.go`, `redact_test.go`, `main.go`).

## 1. Goal

A pure, importable structured-logging package on `log/slog` that:
1. Builds a configured `*slog.Logger` (JSON/text, level, source).
2. Redacts PII automatically from struct fields via tags (`redact`/`mask`/`hash`/`omit`).
3. Correlates every record with `trace_id`/`span_id` when the context carries an active span.
4. Exposes a clean **`WithSink`** seam so `pkg/obsv` can attach its OTel bridge later —
   **without `pkg/logger` taking the OTel SDK.**

## 2. Scope & non-goals

**In scope:** slog construction, handler chain, redaction engine, `Safe()` escape hatch,
level parsing, config struct (tagged), tests + benchmarks.

**Out of scope (lives elsewhere):** OTel provider bootstrap, OTLP exporters, metrics/traces
generation (→ `pkg/obsv`); Cobra flag binding, config loading, feature gating (→ `pkg/bootstrap`);
regex/content-based PII detection (future).

## 3. Dependencies (hard constraint)

`stdlib` + **`go.opentelemetry.io/otel/trace` (API only)** for `SpanContextFromContext`.
No OTel SDK, no exporters, no bridges, no `inovacc/config` import. This keeps the package
near-zero-weight and importable anywhere.

## 4. Public API

```go
package logger

type Format string
const ( FormatJSON Format = "json"; FormatText Format = "text" )

type Config struct {
    ServiceName string    `mapstructure:"service_name" yaml:"service_name" env:"SERVICE_NAME"`
    Level       string    `mapstructure:"level"        yaml:"level"        env:"LEVEL"       default:"info"`
    Format      Format    `mapstructure:"format"       yaml:"format"       env:"FORMAT"      default:"json"`
    AddSource   bool      `mapstructure:"add_source"   yaml:"add_source"   env:"ADD_SOURCE"`
    Redact      bool      `mapstructure:"redact"       yaml:"redact"       env:"REDACT"      default:"true"`
    HashSalt    string    `mapstructure:"hash_salt"    yaml:"hash_salt"    env:"HASH_SALT"   sensitive:"true"`
    Output      io.Writer `mapstructure:"-"            yaml:"-"`   // default os.Stdout
}

type Option func(*options)
func WithSink(h slog.Handler) Option                                  // extra fan-out sink (e.g. obsv bridge)
func WithReplaceAttr(fn func(groups []string, a slog.Attr) slog.Attr) Option
func WithRedactor(r *Redactor) Option                                // advanced: custom redactor

func New(cfg Config, opts ...Option) (*slog.Logger, error)           // pure; no ctx/shutdown
func Init(cfg Config, opts ...Option) (*slog.Logger, error)          // New + slog.SetDefault
func Safe(v any) slog.LogValuer                                      // opt-in redaction wrapper
func SetHashSalt(salt string)                                       // package-level Safe salt
```

**Deltas from the seed:**
- Removed OTel fields (`EnableOTel`, `OTLPEndpoint`, `OTelLoggerName`) from `Config` →
  moved to `obsv.Config`.
- `New` drops `ctx context.Context` and the `ShutdownFunc` return → `(*slog.Logger, error)`.
  (Flush/shutdown is owned by `pkg/obsv`/`pkg/bootstrap`.)
- `Level` is a **string** (`debug|info|warn|error`, case-insensitive) parsed to `slog.Level`,
  for config-file friendliness. Unknown → error from `New`.
- `Redact` **defaults to `true`** (safe-by-default).
- `Config` carries `mapstructure` + `yaml` + `env` + `default` + `sensitive` tags so the
  viper-based `inovacc/config` loads it with zero coupling.
- New `WithSink` / `WithReplaceAttr` / `WithRedactor` options; new `Redactor` exported type
  so `obsv`/tests can share one configured redactor.

## 5. Handler chain (proven design, retained)

```
slog entrypoint
   │
   ▼  redactHandler   ── rewrites attrs/groups, scrubs PII (outermost: no sink sees raw PII)
   ▼  traceHandler    ── injects trace_id/span_id from context's SpanContext (if valid)
   ▼  fanoutHandler   ── clones record to N sinks, errors.Join'd
   ├──────────────► local sink: slog.JSONHandler | slog.TextHandler (Level, AddSource, ReplaceAttr)
   └──────────────► extra sinks via WithSink (e.g. obsv otelslog bridge)   [0..n]
```

**Invariants (test-enforced):**
- Redaction is **outermost** — every sink only ever receives redacted data.
- `fanoutHandler.Enabled` = OR of children; `Handle` fans a cloned `slog.Record` to each;
  `WithAttrs`/`WithGroup` propagate to all children.
- All handlers are immutable/value-safe under `WithAttrs`/`WithGroup` (return new handlers).

## 6. Redaction engine (refactored — pays down seed debt)

**Tag grammar** (struct field tag `pii:"..."`):
| Tag | Effect |
|-----|--------|
| `redact` / `true` | replace value with `[REDACTED]` |
| `mask` / `mask,keep=N` / `mask,N` | reveal last N chars (default 4), star the rest |
| `hash` | `sha256(salt + value)` truncated to 16 hex chars |
| `omit` / `drop` / `-` | field dropped entirely |
| (`json:"-"`) | also treated as omit — API-excluded fields never leak to logs |

**Engine:**
- `Redactor` (exported) holds `salt string`, `maxDepth int` (default 8), and a **single typed
  plan cache** `map[reflect.Type]*structPlan` behind `sync.Map` — replacing the seed's three
  separate caches (`planCache`/`piiTypeCache`/`ifaceTypeCache`).
- **One unified walker** producing `slog.Value` for all kinds (struct→`GroupValue`,
  slice/array→list, map→group), replacing the seed's duplicated `value`/`anyVal` paths.
- `typeHasPII(t)` fast-path (memoised): untagged types pass through with **zero reflection cost**.
- Per-type `structPlan` precomputes field index, output key (honoring `json`/`mapstructure`
  name), `emitKind`, `strategy`, `keep` — amortising tag parsing across calls.
- `maxDepth` bounds recursion (cycle/over-deep protection); interface-typed fields walked
  dynamically to catch hidden PII.
- `Safe(v)` returns a `LogValuer` bound to the package redactor (salt via `SetHashSalt`),
  deferring redaction to handle-time so level-dropped lines pay nothing.

**Resolved seed debt:** unify walkers (1), single cache (2), `Safe()` shares redactor honoring
salt (3). Out of scope this sub-project: middleware composition (4), OTel batch tuning (5).

## 7. Error handling

- `New` returns error on: invalid `Level` string, invalid `Format`, nil `Output` after
  defaulting is impossible (shouldn't happen). No panics on the logging hot path.
- `fanoutHandler.Handle` returns `errors.Join` of all sink errors (one failing sink does not
  suppress others).

## 8. Testing plan (table-driven, ≥80%)

- **redact_test.go** (ported + extended): each strategy (`redact`/`mask,keep`/`hash`/`omit`),
  nested structs, slices/maps, `json:"-"`, interface fields, `maxDepth` cutoff, salt stability.
- **handler_test.go**: redact-before-fanout invariant (capture sink sees only redacted),
  trace_id/span_id injection with/without active span, `WithAttrs`/`WithGroup` propagation
  across fanout, multi-sink error join.
- **logger_test.go**: level parsing (all + invalid), JSON vs text output, `AddSource`,
  `Init` sets default, `Safe()` as `LogValuer`, `SetHashSalt` effect.
- **bench_test.go**: untagged-struct passthrough (assert ~zero alloc), level-dropped `Safe()`
  (assert no reflection), masked vs hash cost.

## 9. File layout (after refactor)

```
pkg/logger/
  logger.go      New, Init, Config, Format, level parsing, options
  handler.go     redactHandler, traceHandler, fanoutHandler
  redact.go      Redactor, structPlan, strategies, unified walker, caches
  safe.go        Safe, safeValue, SetHashSalt, package redactor
  logger_test.go handler_test.go redact_test.go bench_test.go
go.mod           module github.com/inovacc/mantle, go 1.25
LICENSE          BSD-3-Clause
```

(`main.go` seed demo moves to `cmd/logger/` in sub-project B; `otel.go` content moves to
`pkg/obsv` in sub-project B — for sub-project A, `otel.go` is deleted from `pkg/logger` once
`WithSink` lands so the package keeps its API-only OTel dep.)

## 10. Acceptance criteria

1. `go build ./pkg/logger` with deps = stdlib + `otel/trace` only (verified via `go mod graph`).
2. `go test ./pkg/logger -race` green; coverage ≥ 80%.
3. Redact-before-fanout invariant proven by a capturing test sink.
4. `Config` round-trips through a viper/`mapstructure` decode in a test (tags correct).
5. Untagged-struct benchmark shows no measurable reflection overhead vs a raw slog handler.
6. Public API matches §4 exactly (golden API check or doc review).
