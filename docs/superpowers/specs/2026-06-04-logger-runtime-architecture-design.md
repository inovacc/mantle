# Logger Runtime — Architecture Design

- **Date:** 2026-06-04
- **Status:** Approved (shape + sub-project A detail)
- **Module:** `github.com/inovacc/logger` (go 1.25, BSD-3-Clause)
- **Related modules:** `github.com/inovacc/config`, `github.com/inovacc/daemon`

## 1. Problem & Goal

Provide a **reusable application-runtime wrapper** built on `log/slog` that a binary
wires into its Cobra CLI, so that a single config file + feature flags transparently
enables structured logging, PII redaction, full OpenTelemetry observability, and
daemon/service supervision — then hands off to the app's core logic.

The control flow the wrapper enforces:

```
cobra (entry / CLI)  →  THIS module (wrapper / runtime)  →  core app ("other side")
```

Cobra is the front door: it *sets and pre-configures* the wrapper (flags, commands,
subcommands). The wrapper resolves config, evaluates feature flags, builds a `Runtime`
container, and only then invokes the user's core handler. If the app has many commands
and subcommands, each one flows through the same configured wrapper into its core
handler.

## 2. Fleet Context & Conventions (locked)

| Concern        | Convention |
|----------------|------------|
| Module path    | `github.com/inovacc/<name>` |
| Go version     | **1.25** (aligns with `daemon`, which will depend on `logger`) |
| Layout         | Hexagonal: `cmd/<name>` (reference binary), `internal/<subsystem>` (private), `pkg/<name>` (public product) |
| License        | BSD-3-Clause |
| Logging        | `log/slog` only — no third-party logging libs |
| Tests          | Table-driven, **80%+** coverage |

**Sibling modules analyzed.** Note `inovacc/config` exists in **two generations**:
- **Published `inovacc/config` v1.2.2 — TARGET.** Viper-based (vendored viper, `pflag`,
  `mapstructure`, fsnotify). `type Config struct { Version, Environment, AppID,
  AppSecret(sensitive), Logger Logger, Service any }` — a **base config with a built-in
  `Logger` block and a `Service any` slot for the user's own config.** API:
  `InitServiceConfig(userCfg, path)`, `GetServiceConfig[T]()`, `GetBaseConfig()`,
  `SetEnvPrefix`, `AddValidator`, `GetSecureCopy()` (masks `sensitive:"true"`),
  `LogConfig()`, `WatchConfig()` (hot reload), plus `migrate.go` (versioned configs) and
  `encrypt.go` (encrypt-at-rest). **This is the version `daemon` depends on** → fleet-consistent.
- **Local `D:\weaver-sync\modules\config` — the unpublished zero-dep rewrite.** Generic
  `config.New[T]().Load()`, stdlib-only, strict precedence `defaults→file→env→validate`,
  no pflag/viper. Elegant but lacks flag binding and the base/Service split. *Not* the
  integration target for this milestone (revisit if the rewrite ships).

The wrapper integrates with the published viper config **behind a thin internal
`configsource` interface**, so swapping to the zero-dep rewrite later is a one-adapter change.
- `daemon` — supervisor (monitor/worker, restart-guard sliding window + backoff, exit-code
  protocol `0/1/3/4`), `serverinfo` PID file, `kardianos/service` install, Cobra `AttachCommands`.
  `self-spawn`/`self-update` sibling dirs are **empty placeholders**; `ExitUpgrade(4)` and
  self-elevate are scaffolded-but-unimplemented.

## 3. Architecture

```
┌─ CLI layer (Cobra) ───────────────────────────────────────────┐
│ app's cobra root + sub/subcommands                            │
│   bootstrap.Configure(root, Options{...})                     │
│     • binds persistent flags (--config --log-level            │
│       --log-format --log-source --otel --otel-endpoint        │
│       --daemon ...)                                            │
│     • attaches prebuilt cmds (version, service, config)       │
│     • PersistentPreRunE → builds Runtime, stores in ctx       │
└────────────────────────────┬──────────────────────────────────┘
                            │  cobra flags = HIGHEST precedence
┌─ Wrapper / Runtime (THIS module) ─────────────────────────────┐
│ config.Load[App] (defaults → file → env) then flag overlay    │
│ evaluate Features{} and conditionally initialize:             │
│   pkg/logger  → slog + PII redaction + trace correlation      │
│   pkg/obsv    → LoggerProvider + TracerProvider + MeterProvider│
│                 OTLP export, W3C propagators, resource detect, │
│                 Go runtime/host metrics, Tracer()/Meter()      │
│   daemon mode → delegate to inovacc/daemon (monitor/worker,    │
│                 self-spawn / self-update / self-elevate /      │
│                 persistent service)                           │
│ assemble Runtime{ Cfg, Logger, Tracer, Meter, Shutdown,       │
│                   Daemon }                                     │
└────────────────────────────┬──────────────────────────────────┘
                            │  handoff: rt := bootstrap.FromContext(ctx)
┌─ Core app ("the other side") ─────────────────────────────────┐
│ user's real logic: func(ctx, *Runtime) error                 │
└────────────────────────────────────────────────────────────────┘
```

### 3.1 Package layout (`github.com/inovacc/logger`)

```
cmd/logger/            reference binary proving cobra → wrapper → core app
pkg/logger/            slog handler chain, PII redaction, Safe(), Config   (sub-project A)
pkg/obsv/              full OTel bootstrap: 3 providers, OTLP, propagators,  (sub-project B)
                       resource, runtime metrics, Tracer()/Meter() accessors
pkg/bootstrap/         Cobra wrapper, Runtime container, Features gating,   (sub-project B)
                       config+flag precedence, handoff
internal/...           private helpers as needed
docs/                  ARCHITECTURE, ROADMAP, ADRs, specs
```

Plus an extension to the **separate** `github.com/inovacc/daemon` module (sub-project C).

### 3.2 Dependency direction (must hold)

- **Direction is one-way:** `config` never imports `logger`/`obsv`/`otel`. (The published
  viper-based target carries its own deps, but it stays *upstream* of our packages — it only
  reflects over our tagged structs via the `Service any` slot, never importing them.)
- `pkg/logger` deps = stdlib + `go.opentelemetry.io/otel/trace` (**API only**, for
  trace-id correlation; no SDK, no exporters, no config import). `Config` is a plain
  tagged struct the `config` loader reflects over — **no coupling**.
- `pkg/obsv` owns the full OTel SDK + exporters + bridges + contrib instrumentation.
- `pkg/bootstrap` is the only place that imports config + logger + obsv + daemon and
  performs the feature-gated wiring. This is the "config orchestrates optional modules"
  intent, located in a layer that is *allowed* to depend on everything.

### 3.3 Runtime container & handoff contract

```go
type Runtime struct {
    Cfg      any            // the app's loaded typed config (*App)
    Logger   *slog.Logger   // always non-nil (no-op/local if logging disabled)
    Tracer   trace.Tracer   // no-op tracer if observability disabled
    Meter    metric.Meter   // no-op meter if observability disabled
    Shutdown func(context.Context) error  // flushes obsv exporters; always non-nil
    Daemon   *daemon.Controls            // nil unless daemon feature enabled
}

func Configure[T Configurable](root *cobra.Command, app T, opts ...Option) error // CLI setup
func FromContext(ctx context.Context) *Runtime           // handler retrieves runtime
func ConfigOf[T any](rt *Runtime) T                      // typed access to the *App
func Run(cmd *cobra.Command, core func(context.Context, *Runtime) error) error   // handoff
```

`Configure` is generic over `Configurable` (the unexported-method interface satisfied only by
embedding `bootstrap.Base`), registers the always-present flags (§4.2), sets a
`PersistentPreRunE` that loads config (flags > env > file), gates features, builds the
`Runtime`, and stores it in `cmd.Context()`. `Runtime.Cfg` carries the user's `*App`.

**Cobra style: middleware/handoff (idiomatic, chosen).** The app owns its command tree;
`Configure` binds persistent flags + a `PersistentPreRunE` that builds the `Runtime` into
`cmd.Context()`. Each leaf command's `RunE` calls `bootstrap.Run(cmd, coreFn)` (or
`rt := bootstrap.FromContext(ctx)` directly) to reach its core logic. Rejected alternative:
wrapper *owns* a declarative command tree (more magic, less flexible).

## 4. Config & Feature-Flag Model — composite structs + always-present flags

### 4.1 Composite config: wrapper "brings" a `Base`, user extends it

The wrapper **provides** a base config struct; the user **creates their own config for their
needs** by squash-embedding it. The combined struct is registered as the published config's
`Service any` payload (viper/`mapstructure`):

```go
// Provided BY the wrapper (pkg/bootstrap):
type Base struct {
    Features      Features      `mapstructure:"features"      yaml:"features"`
    Logger        logger.Config `mapstructure:"logger"       yaml:"logger"`
    Observability obsv.Config   `mapstructure:"observability" yaml:"observability"`
    Daemon        daemon.Config `mapstructure:"daemon"        yaml:"daemon"`
}
func (b *Base) base() *Base { return b }      // unexported → embedding is enforced

type Features struct {
    Logging       bool `mapstructure:"logging"        yaml:"logging"        default:"true"`
    Observability bool `mapstructure:"observability"  yaml:"observability"`
    Daemon        bool `mapstructure:"daemon"         yaml:"daemon"`
}

// Created BY the user (their app), composing Base + their own fields:
type App struct {
    bootstrap.Base `mapstructure:",squash" yaml:",inline"`
    DBDsn string    `mapstructure:"db_dsn" yaml:"db_dsn"`     // ...their fields
}

// Wiring:
config.InitServiceConfig(&App{ /* defaults */ }, cfgPath)
app, _ := config.GetServiceConfig[*App]()
bootstrap.Configure(root, app)        // extracts app.base() to gate features
```

`Configurable` is an unexported-method interface (`base() *Base`) that **only structs
embedding `bootstrap.Base` can satisfy** — this enforces the composition at compile time and
lets the wrapper reach the base blocks without reflection while still handing the whole
`*App` to the core app.

### 4.2 Always-present bootstrap flags

`bootstrap.Configure` registers these persistent flags on the root command **every time**,
bound through viper (so flags outrank env and file automatically):

| Flag | Maps to | Notes |
|------|---------|-------|
| `-c, --config <path>` | config file location | overrides default search path |
| `--env <name>` | `Config.Environment` | dev/staging/prod |
| `--log-level <lvl>` | `logger.Config.Level` | debug/info/warn/error |
| `-v, --verbose` / `-q, --quiet` | `logger.Config.Level` | shortcuts (debug / error) |
| `--log-format <json\|text>` | `logger.Config.Format` | |
| `--log-source` | `logger.Config.AddSource` | |
| `--no-redact` | `logger.Config.Redact=false` | redaction is on by default |
| `--observability` / `--otel` | `Features.Observability` | enable OTel |
| `--otel-endpoint <addr>` | `obsv.Config.Endpoint` | OTLP target |
| `--otel-protocol <grpc\|http>` | `obsv.Config.Protocol` | |
| `--daemon` | `Features.Daemon` | run under supervisor |
| `--version` | — | print version & exit |

### 4.3 Precedence & gating

**Precedence (top wins):** struct `default:` tags → config file (yaml/json/toml) → env
(`SetEnvPrefix`) → **CLI flags** (viper binds pflags above env/file) → explicit `Set`.
`bootstrap.Configure` reads `Features` after load and initializes only enabled subsystems;
disabled subsystems yield **no-op implementations (never nil)** so core code is branch-free.

## 5. Observability scope (chosen: Full OTel)

`pkg/obsv` bootstraps the complete signal set:
- **LoggerProvider** + `otelslog` bridge handler → plugged into `pkg/logger` via `WithSink`.
- **TracerProvider** with batch span processor; `Tracer()` accessor for app spans.
- **MeterProvider** with periodic reader; `Meter()` accessor; Go runtime + host metrics.
- **OTLP export** over gRPC and HTTP (endpoint-driven; stdout exporter fallback for dev).
- **W3C trace-context + baggage propagators**, **resource auto-detection** (service.name,
  version, host, process), unified `Shutdown(ctx)` that flushes all three providers.

## 6. Daemon integration & gaps to build (sub-project C)

`pkg/bootstrap` can enable "daemon mode" via the `Features.Daemon` flag, delegating to
`inovacc/daemon`'s `AttachCommands`/`RunMonitor`/`RunWorker`. Logger lifecycle hooks emit
structured slog records on: monitor startup (+ PID), restart (attempt + backoff), restart-
loop abort, worker startup (port bind), signal received, graceful-shutdown begin/fallback,
worker exit (+ exit code/reason).

**Gaps to implement in the daemon module:**
- **self-spawn** — robust re-exec of `__monitor`/`__worker` with `<APP>_DAEMON_CHILD` env
  guard to prevent recursive daemonization; strip legacy supervisor env from children.
- **self-update** — realize `ExitUpgrade(4)`: detect replaced binary, monitor re-execs
  itself (`syscall.Exec` on Unix, spawn on Windows), atomic binary swap.
- **self-elevate** — privilege escalation for service install (UAC on Windows, sudo/polkit
  on Unix) when persistent install requires it.
- **persistent** — finalize `kardianos/service` install/uninstall/start/stop/status across
  systemd/launchd/Windows SCM, integrated with serverinfo + lifecycle logging.

These reuse the daemon module's hardened invariants (serverinfo stores monitor PID;
exit-code contract; TOCTOU guard; ports always visible on `__worker`).

## 7. Decomposition & Build Order

| Sub-project | Deliverable | Depends on |
|-------------|-------------|------------|
| **A** | `pkg/logger` — slog + redaction + trace correlation + `Safe()` + `Config` (in-place refactor of the seed already in this dir) | — |
| **B** | `pkg/obsv` (full OTel) + `pkg/bootstrap` (Cobra wrapper, Runtime, Features gating, flag precedence, handoff) + `cmd/logger` reference | A |
| **C** | `daemon` module: self-spawn, self-update, self-elevate, persistent + logger lifecycle hooks; bootstrap daemon-mode enablement | A, B |

Each sub-project gets its own spec → implementation plan → execution cycle. **A is fully
specified** in `2026-06-04-pkg-logger-design.md`. B and C get their specs when reached.

## 8. Non-goals / YAGNI (this milestone)

- Config hot-reload / `watch` (config roadmap, deferred).
- Encrypt-at-rest `secret` codec (config roadmap, deferred).
- Regex-based content PII detection (struct-tag redaction only for now).
- A standalone logging daemon/collector (we export to an external OTLP collector).

## 9. Notes

- This directory is **not** a git repo; specs are written to disk but not committed.
- The seed (`piilog`, go 1.22) is already present in this dir and is refactored in place
  under the `github.com/inovacc/logger` path.
