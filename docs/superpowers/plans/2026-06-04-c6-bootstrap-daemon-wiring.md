# Sub-project C6 — Bootstrap Daemon Wiring

> Part of sub-project C. Target module: **github.com/inovacc/logger** (`pkg/bootstrap` + `cmd/logger`). Depends on the daemon module (local, via replace) at `../daemon` (lifecycle logging already landed in C1).

**Goal:** make daemon mode work end-to-end through Mantle: `Features.Daemon`/`--daemon` runs the core app under the daemon supervisor (monitor+worker), with daemon lifecycle logs flowing through Mantle's redacting logger (via `slog.Default()`). Add `bootstrap.Serve` — a daemon-aware entry point. Non-spawning paths are unit-tested.

**Files:** `go.mod` (+replace), `pkg/bootstrap/base.go`, `pkg/bootstrap/flags.go`, new `pkg/bootstrap/daemon.go`, new `pkg/bootstrap/daemon_test.go`, `cmd/logger/main.go`. Go 1.25.

**Note:** the daemon module is local-only (no published version) → a `replace` directive is required. The monitor re-execs `binary __worker`; the worker child loads config from the default path (`config.yaml`) since `--config` isn't forwarded yet — config-path forwarding is a C2 follow-up.

---

### Task 1: Add the daemon dependency (local replace) + preserve purity

- [ ] **Edit `go.mod`:** add to the require block `github.com/inovacc/daemon v0.0.0-00010101000000-000000000000` and add a replace directive (own line/block): `replace github.com/inovacc/daemon => ../daemon`.
- [ ] Run `go mod tidy` then `go build ./...`. Expected: resolves the local daemon module; OTel pins unchanged.
- [ ] **Purity check (must both print 0):** `go list -deps ./pkg/logger | grep -c "cobra\|daemon\|inovacc/config"` and `go list -deps ./pkg/obsv | grep -c "cobra\|daemon\|inovacc/config"`. If non-zero, STOP.
- [ ] Commit: `git add go.mod go.sum && git commit -m "build(bootstrap): depend on local inovacc/daemon via replace"`.

### Task 2: Add Features.Daemon + --daemon flag

- [ ] **`pkg/bootstrap/base.go`** — add `Daemon` to `Features`:
```go
type Features struct {
	Logging       bool `mapstructure:"logging"       yaml:"logging"`
	Observability bool `mapstructure:"observability" yaml:"observability"`
	Daemon        bool `mapstructure:"daemon"        yaml:"daemon"`
}
```
(`DefaultBase` is unchanged — Daemon defaults false.)

- [ ] **`pkg/bootstrap/flags.go`** — in `registerFlags`, add: `pf.Bool("daemon", false, "run under the daemon supervisor")`. In `overlay`, add a case: `case "daemon": b.Features.Daemon = true`.

### Task 3: `bootstrap.Serve` (daemon-aware entry) + tests

- [ ] **Write `pkg/bootstrap/daemon_test.go`:**
```go
package bootstrap

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func hasCmd(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func TestServeWiresDaemonCommands(t *testing.T) {
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp()
	err := Serve(root, app, func(context.Context, *Runtime) error { return nil },
		WithConfigSource(fakeSource{}), WithAppName("t"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"service", "__monitor", "__worker"} {
		if !hasCmd(root, name) {
			t.Errorf("Serve did not attach %q command", name)
		}
	}
}

func TestServeDirectRunCallsCore(t *testing.T) {
	called := false
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp() // Features.Daemon false
	if err := Serve(root, app, func(ctx context.Context, rt *Runtime) error {
		called = true
		if rt.Logger == nil {
			t.Error("nil logger in core")
		}
		return nil
	}, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatal(err)
	}
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("core not called on direct (non-daemon) run")
	}
}

func TestServeWorkerCommandRunsCore(t *testing.T) {
	called := false
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp()
	if err := Serve(root, app, func(ctx context.Context, rt *Runtime) error {
		called = true
		return nil
	}, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"__worker"}) // worker role runs the core body in-process
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("core not called via __worker command")
	}
}

func TestDaemonFlagOverlay(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--daemon").PersistentFlags(), &b)
	if !b.Features.Daemon {
		t.Error("--daemon should set Features.Daemon")
	}
}
```

- [ ] **Implement `pkg/bootstrap/daemon.go`:**
```go
package bootstrap

import (
	"context"

	"github.com/inovacc/daemon/pkg/daemon"
	"github.com/spf13/cobra"
)

// Serve wires the runtime (like Configure) AND daemon supervision onto root, using
// core as both the direct run body and the daemon worker body.
//
// Without --daemon (Features.Daemon false), the root command runs core directly.
// With --daemon, the root runs core under the daemon supervisor (monitor spawns a
// worker that runs core). The public `service` and hidden `__monitor`/`__worker`
// commands are attached for supervised execution and start/stop/status.
//
// Daemon lifecycle logs use slog.Default(), which Configure installs as Mantle's
// redacting logger — so they are automatically redacted and trace-correlated.
func Serve[T Configurable](root *cobra.Command, app T, core func(context.Context, *Runtime) error, opts ...Option) error {
	if err := Configure(root, app, opts...); err != nil {
		return err
	}
	o := defaultOptions(root)
	for _, fn := range opts {
		fn(&o)
	}
	dopts := daemon.Options{
		BinaryName: o.appName,
		Version:    o.version,
		Serve: func(ctx context.Context, _ daemon.Ports) error {
			return core(ctx, FromContext(ctx))
		},
	}
	if err := daemon.AttachCommands(root, dopts); err != nil {
		return err
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if app.base().Features.Daemon {
			return daemon.RunMonitor(cmd.Context(), dopts)
		}
		return core(cmd.Context(), FromContext(cmd.Context()))
	}
	return nil
}
```

### Task 4: Update `cmd/logger` + verify

- [ ] **Replace `cmd/logger/main.go`'s root wiring** to use `bootstrap.Serve` (demonstrates daemon mode). New body of `main`:
```go
func main() {
	app := &App{Base: bootstrap.DefaultBase(), Greeting: "hello"}

	root := &cobra.Command{
		Use:   "logger-demo",
		Short: "Demonstrates cobra -> bootstrap wrapper -> core app (with optional daemon mode)",
	}

	core := func(ctx context.Context, rt *bootstrap.Runtime) error {
		a := bootstrap.ConfigOf[*App](rt)
		rt.Logger.InfoContext(ctx, "core app running",
			slog.String("greeting", a.Greeting),
			slog.String("env", a.Environment),
		)
		return rt.Shutdown(ctx)
	}

	if err := bootstrap.Serve(root, app, core,
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
(Imports unchanged: context, fmt, log/slog, os, bootstrap, cobra. The `App` type stays.)

- [ ] **Verify:**
```bash
go build ./...
go build ./cmd/logger
go run ./cmd/logger --help        # now also lists `service` + `--daemon`; no config.yaml written
go vet ./...
go test ./pkg/bootstrap/ -race -count=1
go test ./pkg/bootstrap/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1 && rm -f cover.out   # >=80%
go test ./pkg/logger/ ./pkg/obsv/ -race -count=1   # regression
go list -deps ./pkg/logger | grep -c "cobra\|daemon\|inovacc/config"   # 0
```
Expected: all green; purity 0; coverage ≥ 80%; `--help` shows `service` and `--daemon`.

- [ ] **Commit:** `git add pkg/bootstrap/ cmd/logger/main.go && git commit -m "feat(bootstrap): Serve — daemon-aware entry wiring the supervisor + --daemon"` then `git add docs && git commit -m "docs(bootstrap): C6 daemon-wiring plan"`.

---

## Self-Review
- Daemon dep via replace (local) — Task 1; purity re-verified Task 1 + Task 4.
- Features.Daemon + --daemon overlay — Task 2 + `TestDaemonFlagOverlay`.
- `Serve` wires Configure + `AttachCommands` + daemon-aware RunE; lifecycle logs via slog.Default() (C1) — Task 3.
- Tests cover command wiring, direct-run core, __worker→core (in-process), flag overlay. The supervised spawn path (RunMonitor→child) is process-based → exercised only at runtime, not unit-tested (documented).
- cmd/logger demonstrates the full flow — Task 4.
- **Carried-forward:** worker child loads config from default path (no `--config` forwarding yet) → C2; richer daemon config block (ports/datadir) → C2+.
