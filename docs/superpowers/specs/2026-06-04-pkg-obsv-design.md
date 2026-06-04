# Sub-project B1 — `pkg/obsv` Design Spec

- **Date:** 2026-06-04
- **Status:** Approved (design); pending spec review
- **Parent:** `2026-06-04-logger-runtime-architecture-design.md`
- **Package:** `github.com/inovacc/mantle/obsv`
- **Depends on:** `pkg/logger` (only for the integration test via `logger.WithSink`)

## 1. Goal

One call bootstraps the **three OpenTelemetry signal providers** (logs, traces, metrics) +
W3C propagation + a detected resource, and returns a `Stack` whose `LogSink()` plugs into
`logger.WithSink`, with `Tracer()`/`Meter()` accessors and a unified `Shutdown`. This is the
"observability superpowers" layer. When disabled it returns a fully no-op `Stack` (never nil).

## 2. Scope & non-goals

**In scope:** provider bootstrap, OTLP gRPC/HTTP + stdout exporters, resource detection,
sampler, periodic metric reader, Go runtime metrics, propagators, per-signal gating, no-op
path, `Shutdown` flush, the `otelslog` bridge as an `slog.Handler`.

**Out of scope:** Cobra/flags/config-loading (→ `pkg/bootstrap`, B2); custom instrumentation
of app code; collector deployment. `pkg/logger` is untouched (its dep purity must remain).

## 3. Dependencies (resolved & compile-verified via spike)

The module's `go.mod` gains these (pin exactly — versions are cross-checked to compile with
`otel v1.32.0`; note the **v0.x** logs/contrib packages and the **off-by-one** contrib pairing):

```
go.opentelemetry.io/otel v1.32.0
go.opentelemetry.io/otel/sdk v1.32.0
go.opentelemetry.io/otel/sdk/metric v1.32.0
go.opentelemetry.io/otel/sdk/log v0.8.0
go.opentelemetry.io/otel/log v0.8.0
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.32.0
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.32.0
go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.32.0
go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.32.0
go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.8.0
go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.8.0
go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.32.0
go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.32.0
go.opentelemetry.io/otel/exporters/stdout/stdoutlog v0.8.0
go.opentelemetry.io/contrib/bridges/otelslog v0.7.0
go.opentelemetry.io/contrib/instrumentation/runtime v0.57.0
```

`semconv` is the package `go.opentelemetry.io/otel/semconv/v1.26.0` **inside** the otel module
(no separate require). **`go get <otel-submodule>@version` fails** ("does not contain package")
— add `require` lines to `go.mod` and run `go mod tidy`/`go mod download` instead.

**Boundary invariant unchanged:** `go list -deps ./pkg/logger` must STILL show only
`otel/trace` (+ minimal internals). These SDK deps belong to `pkg/obsv`'s import graph only.

## 4. Public API

```go
package obsv

type Config struct {
    Enabled        bool              `mapstructure:"enabled"        yaml:"enabled"`
    Endpoint       string            `mapstructure:"endpoint"       yaml:"endpoint"`        // OTLP host:port; "" → stdout (dev)
    Protocol       string            `mapstructure:"protocol"       yaml:"protocol"        default:"grpc"`  // grpc | http
    Insecure       bool              `mapstructure:"insecure"       yaml:"insecure"`        // skip TLS
    Headers        map[string]string `mapstructure:"headers"        yaml:"headers"`         // OTLP auth headers
    Signals        Signals           `mapstructure:"signals"        yaml:"signals"`
    SampleRatio    float64           `mapstructure:"sample_ratio"   yaml:"sample_ratio"    default:"1.0"`
    MetricInterval time.Duration     `mapstructure:"metric_interval" yaml:"metric_interval" default:"15s"`
    RuntimeMetrics bool              `mapstructure:"runtime_metrics" yaml:"runtime_metrics" default:"true"`
}

// Signals toggles individual pipelines. The zero value (all false) means "all on"
// when Config.Enabled — set one or more true to restrict (e.g. Logs:true => logs only).
type Signals struct {
    Logs    bool `mapstructure:"logs"    yaml:"logs"`
    Traces  bool `mapstructure:"traces"  yaml:"traces"`
    Metrics bool `mapstructure:"metrics" yaml:"metrics"`
}

type ServiceInfo struct{ Name, Version, Environment string }

type Option func(*options)
// WithStdoutWriter routes the dev stdout exporters (used when Endpoint=="") to w.
// Useful for capturing dev telemetry; also the test seam. No effect when Endpoint is set.
func WithStdoutWriter(w io.Writer) Option

type Stack struct{ /* providers + shutdown fns + scope name */ }

func New(ctx context.Context, cfg Config, info ServiceInfo, opts ...Option) (*Stack, error)

func (s *Stack) LogSink() slog.Handler            // otelslog bridge; nil when logs disabled
func (s *Stack) Tracer(name string) trace.Tracer  // no-op tracer when traces disabled
func (s *Stack) Meter(name string)  metric.Meter  // no-op meter when metrics disabled
func (s *Stack) Shutdown(ctx context.Context) error // flush all enabled providers, errors.Join, once
```

`trace` = `go.opentelemetry.io/otel/trace`; `metric` = `go.opentelemetry.io/otel/metric`.

## 5. Behavior

**`New` (enabled):**
1. `normalize(cfg)` — apply defaults (protocol grpc, interval 15s, ratio 1.0, runtime on);
   if `Signals` all-false → all-on.
2. `buildResource(ctx, info)` — `resource.New` with `semconv.ServiceName/ServiceVersion`,
   `attribute.String("deployment.environment", info.Environment)` (when set),
   `resource.WithProcess()/WithHost()/WithTelemetrySDK()/WithFromEnv()`.
3. Per enabled signal, build exporter (Endpoint=="" → stdout[+writer]; else OTLP grpc|http
   with `WithEndpoint/WithInsecure/WithHeaders`), provider, and register the global:
   - **Traces:** `sdktrace.NewTracerProvider(WithResource, WithBatcher(exp), WithSampler(ParentBased(TraceIDRatioBased(ratio))))` → `otel.SetTracerProvider`.
   - **Metrics:** `sdkmetric.NewMeterProvider(WithResource, WithReader(NewPeriodicReader(exp, WithInterval(interval))))` → `otel.SetMeterProvider`; if `RuntimeMetrics` → `runtime.Start(WithMeterProvider(mp))`.
   - **Logs:** `sdklog.NewLoggerProvider(WithResource, WithProcessor(NewBatchProcessor(exp)))` → `global.SetLoggerProvider`; `LogSink = otelslog.NewHandler(scope, WithLoggerProvider(lp))`.
4. Install propagators: `otel.SetTextMapPropagator(NewCompositeTextMapPropagator(TraceContext{}, Baggage{}))`.
5. Record each provider's `Shutdown` into the `Stack`.

**`New` (disabled, `Enabled=false`):** return a no-op `Stack` — `Tracer`→`noop.NewTracerProvider().Tracer`, `Meter`→`noop.NewMeterProvider().Meter`, `LogSink`→`nil`, `Shutdown`→`nil`. No globals touched.

**Per-signal disabled:** that pipeline isn't built; its accessor returns the no-op equivalent (`LogSink` nil). `logger.WithSink(nil)` is already a no-op, so callers can pass `LogSink()` unconditionally.

**`Shutdown`:** `sync.Once`; calls each recorded provider `Shutdown(ctx)`; `errors.Join`. Safe to call repeatedly and on a no-op stack.

## 6. Error handling

`New` returns an error if any enabled exporter/provider fails to construct (wrapped with the
signal name). Partial construction is cleaned up: already-built providers are shut down before
returning the error (no leaked goroutines/exporters). Invalid `Protocol` (not grpc/http) →
error.

## 7. Testing plan (≥75%; exporters are network-bound so transport branches are gated)

- **config_test.go:** `normalize` defaults; all-false Signals → all-on; "logs only"; invalid protocol error.
- **obsv_test.go:**
  - `TestDisabledStackNoops` — `New(Config{Enabled:false})`: `Tracer`/`Meter` non-nil & usable (start a span / record a counter without panic), `LogSink()==nil`, `Shutdown(ctx)==nil`.
  - `TestEnabledStdoutBuilds` — `New(Config{Enabled:true}, …, WithStdoutWriter(&buf))` (Endpoint ""): no error; `LogSink()` non-nil; start+end a span; `Shutdown` flushes; assert `buf` (or a separate captured writer) received trace/log output.
  - `TestLogSinkEmitsThroughBridge` — build stack with `WithStdoutWriter(&buf)`, wrap `logger.New(logger.Config{Output: io.Discard, Redact:true}, logger.WithSink(stack.LogSink()))`, log a PII record, `stack.Shutdown(ctx)`; assert `buf` contains the message and does NOT contain raw SSN (redaction happened before the bridge, since redact is outermost in logger).
  - `TestPerSignalGating` — `Signals{Logs:true}` only: `LogSink()` non-nil but `Meter`/`Tracer` are the no-op kind (recording a counter is a no-op; assert no error + Shutdown clean).
  - `TestInvalidProtocol` — `Protocol:"xml"` with Endpoint set → error.
- Mark transport-specific OTLP-grpc/http construction with focused tests where feasible; the
  network send path is not asserted (no live collector).

## 8. File layout

```
pkg/obsv/
  doc.go         package doc
  config.go      Config, Signals, ServiceInfo, normalize()
  obsv.go        New, Stack, accessors, Shutdown, options
  resource.go    buildResource
  exporters.go   trace/metric/log exporter builders (grpc|http|stdout)
  noop.go        no-op stack helpers
  config_test.go obsv_test.go
```

## 9. Integration contract (for B2 `pkg/bootstrap`)

`bootstrap` will: build `obsv.New(ctx, app.Observability, obsv.ServiceInfo{Name,Version,Environment})`
when `Features.Observability`, then `logger.New(app.Logger, logger.WithSink(stack.LogSink()))`,
expose `Tracer()/Meter()` on the `Runtime`, and register `stack.Shutdown` in the Runtime's
shutdown chain.

## 10. Acceptance criteria

1. `go build ./...` green; `go.mod` has the exact dep set from §3; `go mod tidy` stable.
2. **`go list -deps ./pkg/logger` STILL shows only `otel/trace`** (purity preserved) — verified.
3. `go test ./pkg/obsv -race` green; coverage ≥ 75%.
4. Disabled stack is fully no-op (no globals set; accessors safe; `LogSink()` nil; `Shutdown` nil).
5. `LogSink()` integrates with `logger.WithSink` and a redacted record reaches the bridge with
   no raw PII (proven by `TestLogSinkEmitsThroughBridge`).
6. Public API matches §4 exactly; no over-build.
