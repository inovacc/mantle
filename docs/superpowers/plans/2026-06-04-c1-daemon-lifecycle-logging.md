# Sub-project C1 — Daemon Lifecycle Logging Hooks

> Part of sub-project C (daemon mode). Target module: **github.com/inovacc/daemon** at `D:\weaver-sync\modules\daemon` (baseline commit `01fb84d`). Decision: optional `Options.Logger`, fallback `slog.Default()`.

**Goal:** the supervisor emits structured `slog` lifecycle events (startup, worker-start, worker-exit, restart+backoff, loop-abort, upgrade-requested, shutdown) so consumers — e.g. Mantle wiring its redacting logger as the slog default — get observable, redacted, trace-correlated daemon logs.

**Files:** `pkg/daemon/options.go`, `pkg/daemon/monitor.go`, `pkg/daemon/cobra.go`, new `pkg/daemon/lifecycle_test.go`. Go 1.25, table-driven tests, `-race`.

---

### Task 1: Optional Options.Logger

- [ ] **Add to `pkg/daemon/options.go`:** import `"log/slog"`; add field to `Options` (after `Serve`):
```go
	// Logger receives structured lifecycle events (startup, restart, crash,
	// shutdown, ...). When nil, slog.Default() is used.
	Logger *slog.Logger
```
and a helper:
```go
// logger returns the configured logger, or slog.Default() when none is set.
func (o Options) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}
```

### Task 2: Emit events in the monitor loop

- [ ] **Rewrite `(*monitor).run` in `pkg/daemon/monitor.go`** (add `"log/slog"` import) to:
```go
func (m *monitor) run(ctx context.Context) error {
	log := m.o.logger().With(slog.String("role", "monitor"))
	addr := fmt.Sprintf("localhost:%d", m.o.GRPCPort)
	if err := m.info.Write(serverinfo.Info{
		Address: addr,
		Port:    m.o.GRPCPort,
		PID:     os.Getpid(),
		Version: m.o.Version,
	}); err != nil {
		return fmt.Errorf("write server info: %w", err)
	}
	log.Info("monitor started",
		slog.Int("pid", os.Getpid()), slog.String("version", m.o.Version), slog.String("address", addr))
	defer func() {
		_ = m.info.Remove()
		log.Info("monitor stopped")
	}()

	args := m.o.buildWorkerArgs()
	attempt := 0
	for {
		if ctx.Err() != nil {
			log.Info("monitor stopping", slog.String("reason", "context canceled"))
			return nil
		}
		log.Debug("starting worker", slog.Int("attempt", attempt))
		code := ExitStatus(m.spawn(ctx, args))
		switch code {
		case ExitSuccess:
			log.Info("worker exited cleanly", slog.Int("code", code.AsInt()))
			return nil
		case ExitRestart:
			log.Info("worker requested restart", slog.Int("code", code.AsInt()))
			attempt = 0
			continue
		case ExitUpgrade:
			// Re-exec lands in C3; for now this restarts the worker.
			log.Info("worker requested binary upgrade", slog.Int("code", code.AsInt()))
			attempt = 0
			continue
		default: // ExitError / any crash
			if ctx.Err() != nil {
				log.Info("monitor stopping", slog.String("reason", "context canceled"))
				return nil
			}
			if m.guard.isLoop(time.Now()) {
				log.Error("restart loop detected; aborting",
					slog.Int("crashes", m.o.GuardSize), slog.Duration("window", m.o.GuardWindow))
				return fmt.Errorf("restart loop detected: worker crashed %d times within %s — aborting",
					m.o.GuardSize, m.o.GuardWindow)
			}
			d := m.guard.backoff(attempt)
			log.Warn("worker crashed; restarting",
				slog.Int("code", code.AsInt()), slog.Int("attempt", attempt+1), slog.Duration("backoff", d))
			m.sleep(d)
			attempt++
		}
	}
}
```
Behavior is unchanged except for the added logging and capturing `code` into a var.

### Task 3: Worker logging in RunWorker

- [ ] **Update `RunWorker` in `pkg/daemon/cobra.go`** (add `"log/slog"` import):
```go
func RunWorker(ctx context.Context, opts Options) error {
	o := opts.withDefaults()
	log := o.logger().With(slog.String("role", "worker"))
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("worker serving", slog.Int("http_port", o.HTTPPort), slog.Int("grpc_port", o.GRPCPort))
	err := o.Serve(ctx, Ports{HTTP: o.HTTPPort, GRPC: o.GRPCPort})
	if err != nil {
		log.Error("worker exited with error", slog.Any("err", err))
	} else {
		log.Info("worker exited")
	}
	return err
}
```

### Task 4: Tests — `pkg/daemon/lifecycle_test.go`

```go
package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/inovacc/daemon/pkg/serverinfo"
)

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestMonitorLogsLifecycle(t *testing.T) {
	lg, buf := captureLogger()
	calls := 0
	m := &monitor{
		o:     Options{BinaryName: "t", Logger: lg, GRPCPort: 9501}.withDefaults(),
		guard: newRestartGuard(4, 60*time.Second),
		info:  serverinfo.NewStore(t.TempDir()),
		spawn: func(ctx context.Context, args []string) int {
			calls++
			if calls == 1 {
				return ExitError.AsInt() // crash once...
			}
			return ExitSuccess.AsInt() // ...then clean exit
		},
		sleep: func(time.Duration) {},
	}
	if err := m.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"monitor started", "worker crashed; restarting", "worker exited cleanly", "monitor stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing lifecycle event %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `"role":"monitor"`) {
		t.Error("missing role=monitor attribute")
	}
}

func TestMonitorLoopAbortLogged(t *testing.T) {
	lg, buf := captureLogger()
	m := &monitor{
		o:     Options{BinaryName: "t", Logger: lg}.withDefaults(),
		guard: newRestartGuard(2, time.Hour),
		info:  serverinfo.NewStore(t.TempDir()),
		spawn: func(ctx context.Context, args []string) int { return ExitError.AsInt() },
		sleep: func(time.Duration) {},
	}
	if err := m.run(context.Background()); err == nil {
		t.Fatal("expected loop-abort error")
	}
	if !strings.Contains(buf.String(), "restart loop detected") {
		t.Errorf("loop-abort not logged:\n%s", buf.String())
	}
}

func TestDefaultLoggerFallback(t *testing.T) {
	o := Options{BinaryName: "t"}.withDefaults()
	if o.logger() == nil {
		t.Error("logger() must never return nil")
	}
}
```

### Verify & commit
- [ ] `go build ./...`, `go vet ./...`, `go test ./pkg/daemon/ -race -count=1` (all PASS, incl. the existing monitor tests — behavior unchanged), `go test ./... -race` (full module green).
- [ ] `git add -A && git commit -m "feat(daemon): structured slog lifecycle logging in the supervisor"` (in `D:\weaver-sync\modules\daemon`).

### Notes
- Behavior is preserved exactly (same control flow, exit codes, guard). Only logging is added + `code` captured into a variable.
- `ExitUpgrade` logs "binary upgrade requested" but still restarts — the actual re-exec lands in **C3 (self-update)**.
- This is the seam C2–C5 emit through; consumers (Mantle) wire the redacting logger as slog default before starting the daemon, so these events are auto-redacted/trace-correlated.
