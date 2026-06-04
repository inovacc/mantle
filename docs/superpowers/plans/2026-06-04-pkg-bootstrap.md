# pkg/bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Build `github.com/inovacc/mantle/bootstrap` — the `cobra → wrapper → core-app` runtime: load unified config (defaults→file→env), overlay always-present CLI flags, gate logging+observability behind feature flags, hand the core app a `Runtime` — plus a `cmd/logger` reference binary.

**Architecture:** `Configure[T Configurable](root, app)` registers persistent flags and a `PersistentPreRunE` that loads config via a `ConfigSource` (viper-backed `inovacc/config` by default, injectable fake for tests), overlays changed flags onto the squash-embedded `Base`, builds a `Runtime` (obsv first so its `LogSink` attaches to the logger), and stores it in `cmd.Context()`. Leaf `RunE` calls `bootstrap.Run`/`FromContext`.

**Tech Stack:** Go 1.25, `spf13/cobra`, `inovacc/config v1.2.2` (viper), `pkg/logger`, `pkg/obsv`. Table-driven tests, ≥80% coverage, `-race` clean.

**Spec:** `docs/superpowers/specs/2026-06-04-pkg-bootstrap-design.md`.

---

## File Structure
```
pkg/bootstrap/
  doc.go base.go source.go options.go flags.go runtime.go bootstrap.go
  base_test.go overlay_test.go runtime_test.go viper_test.go
cmd/logger/main.go
```

---

### Task 1: Add cobra + config deps (preserve purity)

**Files:** `go.mod`

- [ ] **Step 1: Add requires**

Append to the `require` block of `go.mod`:
```
	github.com/inovacc/config v1.2.2
	github.com/spf13/cobra v1.8.1
```

- [ ] **Step 2: Resolve and build**

```bash
cd D:/weaver-sync/modules/logger
go mod download
go build ./...
```
Expected: exit 0 (requires present-but-unused is fine). Do NOT `go mod tidy` yet (Task 3 imports them; tidy then keeps everything). If `go build` complains about a missing go.sum entry, run `go mod download` again — do not tidy.

- [ ] **Step 3: Confirm purity unchanged**

```bash
go list -deps ./pkg/logger | grep -c "cobra\|inovacc/config\|otel/sdk\|exporters\|contrib"   # expect 0
go list -deps ./pkg/obsv   | grep -c "cobra\|inovacc/config"                                  # expect 0
```
Both must print `0`. If not, STOP — bootstrap deps must not leak into logger/obsv.

- [ ] **Step 4: Commit**
```bash
git add go.mod go.sum
git commit -m "build(bootstrap): add cobra and inovacc/config dependencies"
```

---

### Task 2: Foundation — `base.go`, `source.go`, `options.go`

**Files:** Create `pkg/bootstrap/{base.go,source.go,options.go,base_test.go}`

- [ ] **Step 1: Write `pkg/bootstrap/base_test.go`**
```go
package bootstrap

import (
	"testing"

	"github.com/inovacc/mantle/logger"
)

// testApp is the in-package fixture: a user app squash-embedding Base.
type testApp struct {
	Base     `mapstructure:",squash" yaml:",inline"`
	Greeting string `mapstructure:"greeting" yaml:"greeting"`
}

func TestDefaultBase(t *testing.T) {
	b := DefaultBase()
	if !b.Features.Logging {
		t.Error("Logging should default on")
	}
	if !b.Logger.Redact {
		t.Error("Redact should default true")
	}
	if b.Logger.Level != "info" {
		t.Errorf("level = %q, want info", b.Logger.Level)
	}
	if b.Logger.Format != logger.FormatJSON {
		t.Errorf("format = %q, want json", b.Logger.Format)
	}
	if !b.Observability.RuntimeMetrics {
		t.Error("RuntimeMetrics should default true")
	}
}

func TestConfigurableSatisfiedByEmbedding(t *testing.T) {
	var c Configurable = &testApp{Base: DefaultBase()}
	if c.base() == nil {
		t.Error("base() should return the embedded Base")
	}
}
```

- [ ] **Step 2: Run → FAIL** (`go test ./pkg/bootstrap/ -run 'DefaultBase|Configurable' -v` → undefined DefaultBase/Base/Configurable).

- [ ] **Step 3: Implement `pkg/bootstrap/base.go`**
```go
package bootstrap

import (
	"time"

	"github.com/inovacc/mantle/logger"
	"github.com/inovacc/mantle/obsv"
)

// Features gates optional subsystems.
type Features struct {
	Logging       bool `mapstructure:"logging"       yaml:"logging"`
	Observability bool `mapstructure:"observability" yaml:"observability"`
}

// Base is the wrapper-provided config block. A user app squash-embeds it:
//
//	type App struct {
//	    bootstrap.Base `mapstructure:",squash" yaml:",inline"`
//	    MyField string `mapstructure:"my_field"`
//	}
type Base struct {
	Environment   string        `mapstructure:"environment"   yaml:"environment"`
	Features      Features      `mapstructure:"features"      yaml:"features"`
	Logger        logger.Config `mapstructure:"logger"        yaml:"logger"`
	Observability obsv.Config   `mapstructure:"observability" yaml:"observability"`
}

func (b *Base) base() *Base { return b }

// Configurable is satisfied only by structs that squash-embed Base (the base()
// method is unexported, so it cannot be implemented outside this package).
type Configurable interface{ base() *Base }

// DefaultBase returns programmatic defaults. The underlying config loader ignores
// `default:` struct tags, so callers seed their app with this before Configure.
func DefaultBase() Base {
	return Base{
		Environment: "dev",
		Features:    Features{Logging: true},
		Logger: logger.Config{
			Level:  "info",
			Format: logger.FormatJSON,
			Redact: true,
		},
		Observability: obsv.Config{
			Protocol:       obsv.ProtocolGRPC,
			SampleRatio:    1.0,
			MetricInterval: 15 * time.Second,
			RuntimeMetrics: true,
		},
	}
}
```

- [ ] **Step 4: Implement `pkg/bootstrap/source.go`**
```go
package bootstrap

import "github.com/inovacc/config"

// ConfigSource loads file+env configuration into app (populated in place). The
// default is viper-backed (inovacc/config); inject a fake in tests via
// WithConfigSource to avoid the loader's process-global state.
type ConfigSource interface {
	Load(app any, path, envPrefix string) error
}

// viperSource is the default, backed by github.com/inovacc/config v1.2.2.
type viperSource struct{}

func (viperSource) Load(app any, path, envPrefix string) error {
	if envPrefix != "" {
		config.SetEnvPrefix(envPrefix)
	}
	// InitServiceConfig stores app as the global Service and unmarshals
	// file+env into it in place (defaults already seeded by the caller).
	return config.InitServiceConfig(app, path)
}
```

- [ ] **Step 5: Implement `pkg/bootstrap/options.go`**
```go
package bootstrap

type options struct {
	source     ConfigSource
	envPrefix  string
	configPath string
	version    string
	appName    string
}

// Option customizes Configure.
type Option func(*options)

// WithConfigSource overrides the default viper-backed config loader (e.g. a fake in tests).
func WithConfigSource(s ConfigSource) Option {
	return func(o *options) {
		if s != nil {
			o.source = s
		}
	}
}

// WithEnvPrefix sets the environment-variable prefix (e.g. "APP" → APP_SERVICE_LOGGER_LEVEL).
func WithEnvPrefix(prefix string) Option { return func(o *options) { o.envPrefix = prefix } }

// WithConfigPath sets the config file path used when --config is not provided.
func WithConfigPath(path string) Option { return func(o *options) { o.configPath = path } }

// WithVersion sets the version reported by --version.
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithAppName sets the service name (OTel resource, version string). Defaults to the root command name.
func WithAppName(name string) Option { return func(o *options) { o.appName = name } }
```

- [ ] **Step 6: Run → PASS** (`go test ./pkg/bootstrap/ -run 'DefaultBase|Configurable' -v`). Then `go vet ./pkg/bootstrap/` and `go build ./...`.

> Note: `go build ./...` succeeds even though `Configure` doesn't exist yet — `base.go`/`source.go`/`options.go` are self-contained. The test only references symbols defined here.

- [ ] **Step 7: Commit**
```bash
git add pkg/bootstrap/base.go pkg/bootstrap/source.go pkg/bootstrap/options.go pkg/bootstrap/base_test.go
git commit -m "feat(bootstrap): Base/Features/Configurable, ConfigSource, options"
```

---

### Task 3: Runtime + Configure — `flags.go`, `runtime.go`, `bootstrap.go`

**Files:** Create `pkg/bootstrap/{flags.go,runtime.go,bootstrap.go,overlay_test.go,runtime_test.go,viper_test.go}`

- [ ] **Step 1: Write the failing tests**

`pkg/bootstrap/overlay_test.go`:
```go
package bootstrap

import (
	"testing"

	"github.com/spf13/cobra"
)

func parsedFlags(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "t"}
	registerFlags(root)
	if err := root.PersistentFlags().Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return root
}

func TestOverlayLevel(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--log-level", "warn").PersistentFlags(), &b)
	if b.Logger.Level != "warn" {
		t.Errorf("level = %q, want warn", b.Logger.Level)
	}
}

func TestOverlayVerboseQuiet(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "-v").PersistentFlags(), &b)
	if b.Logger.Level != "debug" {
		t.Errorf("verbose → %q, want debug", b.Logger.Level)
	}
	b = DefaultBase()
	overlay(parsedFlags(t, "-q").PersistentFlags(), &b)
	if b.Logger.Level != "error" {
		t.Errorf("quiet → %q, want error", b.Logger.Level)
	}
}

func TestOverlayNoRedactAndOtel(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--no-redact", "--otel", "--otel-endpoint", "h:4317", "--otel-protocol", "http").PersistentFlags(), &b)
	if b.Logger.Redact {
		t.Error("--no-redact should disable redaction")
	}
	if !b.Features.Observability || !b.Observability.Enabled {
		t.Error("--otel should enable observability")
	}
	if b.Observability.Endpoint != "h:4317" || b.Observability.Protocol != "http" {
		t.Errorf("otel endpoint/protocol not overlaid: %+v", b.Observability)
	}
}

func TestOverlayNoFlagsKeepsDefaults(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t).PersistentFlags(), &b)
	if b.Logger.Level != "info" || !b.Logger.Redact {
		t.Errorf("defaults changed without flags: %+v", b.Logger)
	}
}
```

`pkg/bootstrap/runtime_test.go`:
```go
package bootstrap

import (
	"context"
	"io"
	"testing"

	"github.com/inovacc/mantle/obsv"
	"github.com/spf13/cobra"
)

type fakeSource struct{}

// Load is a no-op: the app keeps its caller-seeded values (no global state).
func (fakeSource) Load(app any, path, envPrefix string) error { return nil }

func defaultTestApp() *testApp {
	a := &testApp{Base: DefaultBase(), Greeting: "hi"}
	a.Logger.Output = io.Discard // keep test stdout clean
	return a
}

func runWith(t *testing.T, app *testApp) *Runtime {
	t.Helper()
	var captured *Runtime
	root := &cobra.Command{Use: "t"}
	sub := &cobra.Command{Use: "go", RunE: func(cmd *cobra.Command, args []string) error {
		captured = FromContext(cmd.Context())
		return nil
	}}
	root.AddCommand(sub)
	if err := Configure(root, app, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	root.SetArgs([]string{"go"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured == nil {
		t.Fatal("runtime not captured")
	}
	return captured
}

func TestConfigureBuildsRuntime(t *testing.T) {
	app := defaultTestApp()
	app.Features.Observability = true
	app.Observability.RuntimeMetrics = false        // no background goroutine in tests
	app.Observability.Signals = obsv.Signals{Traces: true} // minimal, quiet
	rt := runWith(t, app)
	if rt.Logger == nil || rt.Tracer == nil || rt.Meter == nil || rt.Shutdown == nil {
		t.Fatal("runtime fields must be non-nil")
	}
	if got := ConfigOf[*testApp](rt); got == nil || got.Greeting != "hi" {
		t.Errorf("ConfigOf returned %v", got)
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestFeatureGatingLoggingOnly(t *testing.T) {
	app := defaultTestApp() // Logging on, Observability off
	rt := runWith(t, app)
	if rt.Logger == nil || rt.Tracer == nil || rt.Meter == nil {
		t.Fatal("accessors must be non-nil even with observability off")
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown should be nil with observability off: %v", err)
	}
}

func TestFromContextNoRuntime(t *testing.T) {
	rt := FromContext(context.Background())
	if rt == nil || rt.Logger == nil || rt.Tracer == nil || rt.Meter == nil || rt.Shutdown == nil {
		t.Fatal("FromContext must return a safe no-op runtime (never nil)")
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown: %v", err)
	}
}

func TestRunHandoff(t *testing.T) {
	called := false
	root := &cobra.Command{Use: "t"}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return Run(cmd, func(ctx context.Context, rt *Runtime) error {
			called = true
			if rt.Logger == nil {
				t.Error("rt.Logger nil in core handler")
			}
			return nil
		})
	}
	app := defaultTestApp()
	if err := Configure(root, app, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatal(err)
	}
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("core func was not called")
	}
}
```

`pkg/bootstrap/viper_test.go`:
```go
package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestViperSourceLoadsYAML is the single integration test that exercises the real
// viper-backed ConfigSource (and the loader's process-global state). Self-contained.
func TestViperSourceLoadsYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "service:\n  logger:\n    level: warn\n  greeting: from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app := defaultTestApp() // level info, greeting hi
	root := &cobra.Command{Use: "t", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	if err := Configure(root, app, WithAppName("t"), WithConfigPath(path)); err != nil {
		t.Fatal(err)
	}
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if app.Logger.Level != "warn" {
		t.Errorf("file did not override level: %q", app.Logger.Level)
	}
	if app.Greeting != "from-file" {
		t.Errorf("file did not set greeting: %q", app.Greeting)
	}
}
```

- [ ] **Step 2: Run → FAIL** (`go test ./pkg/bootstrap/ -run 'Overlay|Configure|Gating|FromContext|RunHandoff|Viper' -v` → undefined registerFlags/overlay/Configure/Runtime/FromContext/Run/ConfigOf).

- [ ] **Step 3: Implement `pkg/bootstrap/flags.go`**
```go
package bootstrap

import (
	"github.com/inovacc/mantle/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// registerFlags adds the always-present persistent flags to root.
func registerFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	pf.StringP("config", "c", "", "config file path")
	pf.String("env", "", "environment (dev|staging|prod)")
	pf.String("log-level", "", "log level (debug|info|warn|error)")
	pf.BoolP("verbose", "v", false, "verbose logging (debug level)")
	pf.BoolP("quiet", "q", false, "quiet logging (error level)")
	pf.String("log-format", "", "log format (json|text)")
	pf.Bool("log-source", false, "include source file:line in logs")
	pf.Bool("no-redact", false, "disable PII redaction")
	pf.Bool("otel", false, "enable OpenTelemetry observability")
	pf.String("otel-endpoint", "", "OTLP endpoint host:port")
	pf.String("otel-protocol", "", "OTLP protocol (grpc|http)")
}

// overlay applies CHANGED flags onto b (highest precedence). pflag.FlagSet.Visit
// visits only flags the user actually set.
func overlay(fs *pflag.FlagSet, b *Base) {
	fs.Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "env":
			b.Environment = f.Value.String()
		case "log-level":
			b.Logger.Level = f.Value.String()
		case "verbose":
			b.Logger.Level = "debug"
		case "quiet":
			b.Logger.Level = "error"
		case "log-format":
			b.Logger.Format = logger.Format(f.Value.String())
		case "log-source":
			b.Logger.AddSource = true
		case "no-redact":
			b.Logger.Redact = false
		case "otel":
			b.Features.Observability = true
			b.Observability.Enabled = true
		case "otel-endpoint":
			b.Observability.Endpoint = f.Value.String()
		case "otel-protocol":
			b.Observability.Protocol = f.Value.String()
		}
	})
}
```

- [ ] **Step 4: Implement `pkg/bootstrap/runtime.go`**
```go
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/inovacc/mantle/logger"
	"github.com/inovacc/mantle/obsv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Runtime is what the wrapper hands the core app.
type Runtime struct {
	Cfg      any                         // the user's *App
	Logger   *slog.Logger                // always non-nil
	Tracer   trace.Tracer                // no-op when observability is off
	Meter    metric.Meter                // no-op when observability is off
	Shutdown func(context.Context) error // flushes observability; always non-nil
}

type runtimeKey struct{}

func withRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// FromContext returns the Runtime stored by Configure, or a safe no-op Runtime
// (never nil) if none is present.
func FromContext(ctx context.Context) *Runtime {
	if rt, ok := ctx.Value(runtimeKey{}).(*Runtime); ok && rt != nil {
		return rt
	}
	return noopRuntime()
}

func noopRuntime() *Runtime {
	return &Runtime{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:   tracenoop.NewTracerProvider().Tracer("noop"),
		Meter:    metricnoop.NewMeterProvider().Meter("noop"),
		Shutdown: func(context.Context) error { return nil },
	}
}

// ConfigOf returns the typed config from the Runtime (the *App passed to Configure).
func ConfigOf[T any](rt *Runtime) T {
	v, _ := rt.Cfg.(T)
	return v
}

// Run retrieves the Runtime from the command context and invokes the core handler.
func Run(cmd *cobra.Command, core func(context.Context, *Runtime) error) error {
	ctx := cmd.Context()
	return core(ctx, FromContext(ctx))
}

// buildRuntime constructs observability first (so its LogSink can attach to the
// logger), then the logger, and a combined Shutdown.
func buildRuntime(ctx context.Context, b *Base, o options, cfg any) (*Runtime, error) {
	rt := &Runtime{Cfg: cfg, Shutdown: func(context.Context) error { return nil }}
	var sink slog.Handler
	var shutdowns []func(context.Context) error

	if b.Features.Observability {
		b.Observability.Enabled = true
		stack, err := obsv.New(ctx, b.Observability, obsv.ServiceInfo{
			Name:        o.appName,
			Version:     o.version,
			Environment: b.Environment,
		})
		if err != nil {
			return nil, fmt.Errorf("bootstrap: observability: %w", err)
		}
		sink = stack.LogSink()
		rt.Tracer = stack.Tracer(o.appName)
		rt.Meter = stack.Meter(o.appName)
		shutdowns = append(shutdowns, stack.Shutdown)
	} else {
		rt.Tracer = tracenoop.NewTracerProvider().Tracer(o.appName)
		rt.Meter = metricnoop.NewMeterProvider().Meter(o.appName)
	}

	if b.Features.Logging {
		lg, err := logger.Init(b.Logger, logger.WithSink(sink)) // WithSink(nil) is a no-op
		if err != nil {
			return nil, fmt.Errorf("bootstrap: logger: %w", err)
		}
		rt.Logger = lg
	} else {
		rt.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if len(shutdowns) > 0 {
		rt.Shutdown = func(c context.Context) error {
			var errs error
			for i := len(shutdowns) - 1; i >= 0; i-- {
				errs = errors.Join(errs, shutdowns[i](c))
			}
			return errs
		}
	}
	return rt, nil
}
```

- [ ] **Step 5: Implement `pkg/bootstrap/bootstrap.go`**
```go
package bootstrap

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Configure wires the always-present flags and a PersistentPreRunE onto root that
// loads config, overlays flags, builds the Runtime, and stores it in the command
// context. app must squash-embed Base (Configurable). T is typically *App.
func Configure[T Configurable](root *cobra.Command, app T, opts ...Option) error {
	o := defaultOptions(root)
	for _, fn := range opts {
		fn(&o)
	}
	if o.version != "" {
		root.Version = o.version
	}
	registerFlags(root)

	prev := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if prev != nil {
			if err := prev(cmd, args); err != nil {
				return err
			}
		}
		path := o.configPath
		if cp, _ := cmd.Flags().GetString("config"); cp != "" {
			path = cp
		}
		if err := o.source.Load(any(app), path, o.envPrefix); err != nil {
			return fmt.Errorf("bootstrap: load config: %w", err)
		}
		b := app.base()
		overlay(cmd.Flags(), b)
		rt, err := buildRuntime(cmd.Context(), b, o, any(app))
		if err != nil {
			return err
		}
		cmd.SetContext(withRuntime(cmd.Context(), rt))
		return nil
	}
	return nil
}

func defaultOptions(root *cobra.Command) options {
	return options{
		source:     viperSource{},
		configPath: "config.yaml",
		version:    "dev",
		appName:    root.Name(),
	}
}
```

- [ ] **Step 6: Resolve deps and run the suite**
```bash
go mod tidy
go test ./pkg/bootstrap/ -race -count=1 -v
go vet ./pkg/bootstrap/
go build ./...
```
Expected: `go mod tidy` keeps the otel pins (otelslog v0.7.0 etc.) and adds cobra/config + their transitive deps; all bootstrap tests PASS; `-race` clean; vet/build clean. If `TestViperSourceLoadsYAML` fails to override the level, confirm the YAML has the `service:` top-level key (the loader stores the app under `service:`).

- [ ] **Step 7: Commit**
```bash
git add pkg/bootstrap/flags.go pkg/bootstrap/runtime.go pkg/bootstrap/bootstrap.go pkg/bootstrap/overlay_test.go pkg/bootstrap/runtime_test.go pkg/bootstrap/viper_test.go go.mod go.sum
git commit -m "feat(bootstrap): Configure, Runtime, flag overlay, feature gating, handoff"
```

---

### Task 4: Reference binary + doc + verification

**Files:** Create `cmd/logger/main.go`, `pkg/bootstrap/doc.go`; update `README.md`

- [ ] **Step 1: Implement `cmd/logger/main.go`**
```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/inovacc/mantle/bootstrap"
	"github.com/spf13/cobra"
)

// App composes the wrapper's Base with the app's own fields.
type App struct {
	bootstrap.Base `mapstructure:",squash" yaml:",inline"`
	Greeting       string `mapstructure:"greeting" yaml:"greeting"`
}

func main() {
	app := &App{Base: bootstrap.DefaultBase(), Greeting: "hello"}

	root := &cobra.Command{
		Use:   "logger-demo",
		Short: "Demonstrates cobra -> bootstrap wrapper -> core app",
		RunE: func(cmd *cobra.Command, args []string) error {
			return bootstrap.Run(cmd, func(ctx context.Context, rt *bootstrap.Runtime) error {
				a := bootstrap.ConfigOf[*App](rt)
				rt.Logger.InfoContext(ctx, "core app running",
					slog.String("greeting", a.Greeting),
					slog.String("env", a.Environment),
				)
				return rt.Shutdown(ctx)
			})
		},
	}

	if err := bootstrap.Configure(root, app,
		bootstrap.WithAppName("logger-demo"),
		bootstrap.WithVersion("0.1.0"),
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Implement `pkg/bootstrap/doc.go`**
```go
// Package bootstrap wires a Cobra CLI to a core application: it loads unified
// config (defaults -> file -> env), overlays always-present flags, gates optional
// subsystems (logging, observability) behind feature flags, and hands the core
// app a Runtime (logger, tracer, meter, shutdown). The flow is cobra -> wrapper
// -> core app.
//
//	type App struct {
//	    bootstrap.Base `mapstructure:",squash" yaml:",inline"`
//	    Greeting string `mapstructure:"greeting"`
//	}
//	app := &App{Base: bootstrap.DefaultBase()}
//	root := &cobra.Command{Use: "myapp", RunE: func(cmd *cobra.Command, _ []string) error {
//	    return bootstrap.Run(cmd, func(ctx context.Context, rt *bootstrap.Runtime) error {
//	        rt.Logger.InfoContext(ctx, "hello")
//	        return rt.Shutdown(ctx)
//	    })
//	}}
//	bootstrap.Configure(root, app, bootstrap.WithAppName("myapp"))
//	root.Execute()
//
// Defaults are applied programmatically via DefaultBase (the loader ignores
// `default:` struct tags). CLI flags outrank file and env. Daemon mode arrives
// in a later milestone.
package bootstrap
```

- [ ] **Step 3: Append to `README.md`**

After the obsv section, add:
```markdown
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
```

- [ ] **Step 4: Verify**
```bash
go build ./...
go build ./cmd/logger
go run ./cmd/logger --help        # lists the always-present flags; does NOT create a config file
go vet ./...
go test ./pkg/bootstrap/ -race -coverprofile=cover.out && go tool cover -func=cover.out | tail -1 && rm -f cover.out
go test ./pkg/logger/ ./pkg/obsv/ -race -count=1   # regression
go list -deps ./pkg/logger | grep -c "cobra\|inovacc/config"   # expect 0
```
Expected: all green; bootstrap coverage ≥ 80%; logger/obsv still pass; purity count `0`. (`--help` short-circuits before PersistentPreRunE, so no `config.yaml` is written into the repo.)

- [ ] **Step 5: Commit (code + planning docs)**
```bash
git add cmd/logger/main.go pkg/bootstrap/doc.go README.md
git commit -m "feat(bootstrap): cmd/logger reference binary; package doc; README"
git add docs
git commit -m "docs(bootstrap): add pkg-bootstrap design spec and implementation plan"
```

---

## Self-Review

**Spec coverage** (`2026-06-04-pkg-bootstrap-design.md`):
- §3 deps + purity → Task 1 (add) + Task 4 (re-verify). ✓
- §5 API (`Base`/`Features`/`Configurable`/`DefaultBase`/`ConfigSource`/`Runtime`/`Configure`/`FromContext`/`ConfigOf`/`Run`/options) → Tasks 2–3. ✓
- §6 flags + overlay precedence → `flags.go` + overlay_test. ✓
- §7 Configure/buildRuntime flow (obsv-before-logger, sink attach, no-ops) → runtime.go + bootstrap.go. ✓
- §8 tests (DefaultBase, overlay, fake-source runtime, no-runtime, handoff, viper integration) → Tasks 2–3. ✓
- §9 file layout → matches. ✓
- §10 acceptance: build/tidy (T1,T3), purity (T1,T4), race+cov≥80% (T3,T4), overlay precedence (T3), gating (T3), cmd/logger (T4), API match. ✓

**Placeholder scan:** none.

**Type consistency:** `Configure[T Configurable]` + `app.base()`; `viperSource`/`fakeSource` both satisfy `ConfigSource`; `buildRuntime(ctx,*Base,options,any)` matches the call in `bootstrap.go`; `overlay(*pflag.FlagSet,*Base)` matches `registerFlags`; `Runtime`/`FromContext`/`ConfigOf`/`Run` consistent; `obsv.ServiceInfo`/`obsv.New`/`stack.LogSink/Tracer/Meter/Shutdown` and `logger.Init`/`logger.WithSink` match the as-built APIs of sub-projects A and B1.

**Carried-forward note:** daemon mode (`--daemon`, `Features.Daemon`, supervisor) intentionally absent — sub-project C.
