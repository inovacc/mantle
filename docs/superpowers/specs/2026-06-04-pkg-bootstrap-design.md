# Sub-project B2 — `pkg/bootstrap` Design Spec

- **Date:** 2026-06-04
- **Status:** Approved (design); pending spec review
- **Parent:** `2026-06-04-logger-runtime-architecture-design.md`
- **Package:** `github.com/inovacc/mantle/bootstrap` + `cmd/logger`
- **Depends on:** `pkg/logger`, `pkg/obsv`, `github.com/inovacc/config` v1.2.2, `github.com/spf13/cobra`

## 1. Goal

The `cobra → wrapper → core-app` runtime. A binary calls `bootstrap.Configure(root, app)` to
register always-present flags and a `PersistentPreRunE` that: loads config (defaults → file →
env), overlays changed CLI flags (highest precedence), evaluates `Features`, builds a `Runtime`
(logger + observability + shutdown), and stores it in the command context. Each leaf command's
`RunE` retrieves the `Runtime` and hands off to the core app.

## 2. Scope & non-goals

**In scope:** composite `Base` config the user squash-embeds; `Configurable` enforcement;
programmatic defaults; `ConfigSource` abstraction (viper-backed default + injectable fake);
always-present flags + manual overlay; `Features` gating for **logging + observability**; the
`Runtime` container + `FromContext`/`ConfigOf`/`Run`; a `version` affordance; the `cmd/logger`
reference binary.

**Out of scope (→ sub-project C):** daemon mode (`--daemon`, `Features.Daemon`, supervisor
wiring, self-spawn/update/elevate). `pkg/bootstrap` does **not** import `inovacc/daemon` yet.

## 3. Dependencies

Adds to `go.mod`: `github.com/spf13/cobra` (latest v1.8.x), `github.com/inovacc/config v1.2.2`
(the version `daemon` uses — viper-based). Plus `otel/trace`, `otel/metric` (+ their `/noop`)
for `Runtime` accessor types (already in the module via obsv).

**Purity invariant:** `pkg/logger` and `pkg/obsv` must NOT gain `cobra`/`config` in their
import graphs. Only `pkg/bootstrap` (and `cmd/logger`) import them. Re-verify
`go list -deps ./pkg/logger` (only `otel/trace`) and that `./pkg/obsv` doesn't import `config`/`cobra`.

## 4. Config integration recipe (from research on published `inovacc/config` v1.2.2)

Constraints the design accommodates:
- **No public pflag binding** → flags are applied by a **manual post-load overlay** (`pflag.FlagSet.Visit` only visits *changed* flags).
- **`default:` struct tags are ignored** by the loader → defaults are applied **programmatically** via `DefaultBase()` (the user seeds their `App` with it before `Configure`).
- **`InitServiceConfig(&app, path)` populates `app` in place** (stores the pointer, unmarshals file+env via mapstructure) → no need for `GetServiceConfig[T]`.
- **Squash needs both** `mapstructure:",squash"` and `yaml:",inline"` on the embedded `Base`.
- **Global singleton** → the `ConfigSource` interface isolates this so tests use a fake and never touch global state.
- Files are **yaml/yml/json** only; `configPath` is a full file path; missing file is auto-created (not an error).

## 5. Public API

```go
package bootstrap

type Features struct {
    Logging       bool `mapstructure:"logging"       yaml:"logging"`
    Observability bool `mapstructure:"observability" yaml:"observability"`
}

type Base struct {
    Environment   string        `mapstructure:"environment"   yaml:"environment"`
    Features      Features      `mapstructure:"features"      yaml:"features"`
    Logger        logger.Config `mapstructure:"logger"        yaml:"logger"`
    Observability obsv.Config   `mapstructure:"observability" yaml:"observability"`
}
func (b *Base) base() *Base { return b }

// Configurable is satisfied only by structs that squash-embed Base (the base()
// method is unexported, so it can't be implemented outside this package).
type Configurable interface{ base() *Base }

// DefaultBase returns programmatic defaults (config's default: tags are ignored):
// Features.Logging on, JSON logger at info with redaction on, observability off
// but pre-tuned (grpc, ratio 1.0, 15s, runtime metrics on).
func DefaultBase() Base

// ConfigSource loads file+env config into app (populated in place). The default
// is viper-backed (inovacc/config); inject a fake in tests via WithConfigSource.
type ConfigSource interface{ Load(app any, path, envPrefix string) error }

type Runtime struct {
    Cfg      any                          // the user's *App
    Logger   *slog.Logger                 // always non-nil
    Tracer   trace.Tracer                 // no-op when observability off
    Meter    metric.Meter                 // no-op when observability off
    Shutdown func(context.Context) error  // flushes obsv; always non-nil
}

type Option func(*options)
func WithConfigSource(s ConfigSource) Option
func WithEnvPrefix(prefix string) Option
func WithConfigPath(path string) Option     // default path when --config absent
func WithVersion(v string) Option
func WithAppName(name string) Option

func Configure[T Configurable](root *cobra.Command, app T, opts ...Option) error
func FromContext(ctx context.Context) *Runtime   // never nil (no-op Runtime if absent)
func ConfigOf[T any](rt *Runtime) T
func Run(cmd *cobra.Command, core func(context.Context, *Runtime) error) error
```

`trace`/`metric` = `go.opentelemetry.io/otel/{trace,metric}`.

## 6. Always-present flags & overlay (highest precedence)

`Configure` registers these persistent flags on `root`; the overlay maps *changed* flags onto
`Base` after config load:

| Flag | Overlay effect |
|------|----------------|
| `-c, --config string` | sets the config file path used for loading (applied before load) |
| `--env string` | `Base.Environment` |
| `--log-level string` | `Base.Logger.Level` |
| `-v, --verbose` | `Base.Logger.Level = "debug"` |
| `-q, --quiet` | `Base.Logger.Level = "error"` |
| `--log-format string` | `Base.Logger.Format` |
| `--log-source` | `Base.Logger.AddSource = true` |
| `--no-redact` | `Base.Logger.Redact = false` |
| `--otel` | `Base.Features.Observability = true`, `Base.Observability.Enabled = true` |
| `--otel-endpoint string` | `Base.Observability.Endpoint` |
| `--otel-protocol string` | `Base.Observability.Protocol` |
| `--version` | via `root.Version` (cobra prints & exits) |

Precedence realized: `DefaultBase()` → file → env (all inside `ConfigSource.Load`) → **flag
overlay** (here) → `Runtime`.

## 7. `Configure` / `buildRuntime` flow

`Configure[T]` sets `root.Version`, registers flags, and wraps `root.PersistentPreRunE`
(chaining any existing one):
1. Resolve path: `--config` flag value, else `WithConfigPath`, else a sensible default.
2. `source.Load(app, path, envPrefix)` — populates `app` (defaults already seeded by caller).
3. `b := app.base()`; `overlay(cmd.Flags(), b)`.
4. `rt := buildRuntime(ctx, b, opts, app)`; `cmd.SetContext(withRuntime(ctx, rt))`.

`buildRuntime` (observability before logger so the bridge can attach):
- `Features.Observability` → `obsv.New(ctx, b.Observability, ServiceInfo{appName, version, b.Environment})`; `sink = stack.LogSink()`; `rt.Tracer/Meter = stack.Tracer/Meter(appName)`; record `stack.Shutdown`. Else `rt.Tracer/Meter` = OTel no-ops.
- `Features.Logging` → `logger.Init(b.Logger, logger.WithSink(sink))` (sink nil-safe) → `rt.Logger`. Else `rt.Logger` = discard logger.
- `rt.Shutdown` chains recorded shutdowns (reverse, `errors.Join`); no-op when none.

Errors from `obsv.New`/`logger.Init` abort `PersistentPreRunE` with a wrapped error.

## 8. Testing plan (≥80% on bootstrap logic; global-singleton paths isolated)

- **base_test.go:** `DefaultBase` invariants (Redact true, level info, format json, obsv pre-tuned, Logging on); `Configurable` satisfied by a squash-embedding `App`.
- **overlay_test.go:** drive a `pflag.FlagSet` with each flag set → assert the right `Base` field changed; unset flags leave defaults; `--no-redact`, `-v/-q`, `--otel` behavior.
- **runtime_test.go (fake source):**
  - `TestConfigureBuildsRuntime` — `Configure(root, app, WithConfigSource(fake), WithAppName...)`, execute a subcommand whose `RunE` captures `FromContext`; assert non-nil `Logger/Tracer/Meter/Shutdown` and `ConfigOf[*App]` returns the app.
  - `TestFeatureGatingLoggingOnly` — Logging only → real logger, no-op Tracer/Meter, `Shutdown` nil-safe.
  - `TestObservabilityWiresSink` — Logging+Observability (stdout obsv) → logger emits and the obsv `LogSink` is attached (a redacted record reaches it; reuse obsv's stdout writer seam if needed) OR assert `rt.Tracer` is a real (non-noop) tracer.
  - `TestFromContextNoRuntime` — `FromContext(context.Background())` returns a safe no-op Runtime (never nil).
  - `TestRunHandoff` — `Run` invokes the core func with the runtime.
- **viper_test.go (1 isolated integration):** write a temp `config.yaml` with a `logger.level` and an app field; use the **real** viper `ConfigSource`; assert the value loaded. This is the only test touching the global singleton — keep it self-contained (temp dir, unique).
- **cmd/logger** builds and `go run ./cmd/logger --help` lists the always-present flags.

## 9. File layout

```
pkg/bootstrap/
  doc.go         package doc
  base.go        Base, Features, Configurable, DefaultBase
  source.go      ConfigSource, viperSource (inovacc/config), default path
  options.go     options, Option setters
  flags.go       registerFlags, overlay
  runtime.go     Runtime, buildRuntime, withRuntime, FromContext, ConfigOf, Run
  bootstrap.go   Configure
  base_test.go overlay_test.go runtime_test.go viper_test.go
cmd/logger/
  main.go        reference binary: cobra → Configure → core app
```

## 10. Acceptance criteria

1. `go build ./...` green; `go.mod` gains `cobra` + `config v1.2.2`; `go mod tidy` stable.
2. **Purity preserved:** `go list -deps ./pkg/logger` only `otel/trace`; `./pkg/obsv` imports neither `cobra` nor `config`.
3. `go test ./pkg/bootstrap -race` green; coverage ≥ 80% (bootstrap logic; the viper integration test included).
4. Flag overlay precedence proven (a `--log-level` overrides the loaded/default value).
5. Feature gating proven: observability-off yields no-op Tracer/Meter and a logger with no OTel sink; both-on wires the sink and real providers.
6. `cmd/logger` demonstrates `cobra → wrapper → core` and runs (`--help`, a basic run).
7. Public API matches §5 exactly; daemon concerns absent (deferred to C).
