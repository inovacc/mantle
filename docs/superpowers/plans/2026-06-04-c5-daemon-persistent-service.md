# C5 Persistent OS Service (`svc` group) Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox syntax.

**Goal:** Add a NEW `svc` command group to the `github.com/inovacc/daemon` module that registers the daemon as a real **OS-managed service** (Windows SCM / systemd / launchd) via `github.com/kardianos/service` v1.2.2. The kardianos *program* wraps the existing supervisor (`RunMonitor`), so the OS service manager actually runs the real monitor→worker chain. The existing C2 `service` group (lightweight background daemonize) is left **completely untouched**. The `cmd/daemon` demo binary is refactored from its hand-rolled kardianos stub to consume `daemon.AttachCommands`, so it gets the `service`, `svc`, `__monitor`, and `__worker` commands for free, and maps exit codes via `daemon.ExitCodeFor`.

**Architecture:** All new OS-service logic lives **inside `pkg/daemon`** (package `daemon`) so it is reusable and Mantle's `bootstrap.Serve` inherits it through `AttachCommands`. A new file `svc.go` defines the kardianos `program` (Start launches `RunMonitor` in a cancelable goroutine; Stop cancels and waits up to 10s) and the `svc` cobra group with verbs `install`/`uninstall`/`start`/`stop`/`restart`/`status`/`run`. Testability comes from an **unexported `osService` interface** (Install/Uninstall/Start/Stop/Restart/Status/Run) that the real `service.Service` satisfies, constructed through a package-var seam `newOSService = realOSService`; unit tests inject a fake and assert each verb dispatches to the right method. `AttachCommands` is extended to also add the `svc` group (the existing `service`/`__monitor`/`__worker` wiring is preserved verbatim). A minimal `ExitCodeFor(err) int` mapper is added so the demo's `Execute()` can map daemon errors to process exit codes; C4 later extends both `exitstatus.go` and `ExitCodeFor` with `ExitNeedsPrivilege=5` and prepends `RequirePrivilege` to the mutating verbs' `RunE`. The kardianos syscalls themselves are only **build-verified** (cross-compiled for windows/linux/darwin); orchestration is unit-tested through the seam.

**Tech Stack:** Go 1.25, `github.com/spf13/cobra` v1.8.0, `github.com/kardianos/service` v1.2.2 (already a direct require in the daemon `go.mod`), `golang.org/x/sys` v0.34.0 (already pinned), stdlib `context`/`fmt`/`time`/`log/slog`/`os`. **No new module download** — kardianos and x/sys are already present. The `kardianos` import is permitted inside `pkg/daemon` (the daemon module may use it); the logger module's `pkg/logger` and `pkg/obsv` must continue to import **zero** kardianos.

---

### Task 1: Define the testable `osService` seam and the kardianos `program`

**Files:**
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/svc.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/svc_test.go` (Create)

This task introduces the seam and the program wrapper only. The cobra `svc` group is added in Task 2, and `AttachCommands` wiring in Task 3.

- [ ] **Step 1: Write a failing test for the `program` lifecycle (Start launches the supervisor, Stop cancels and returns within the budget).**

`program.run` is the injectable supervisor seam (defaults to `RunMonitor`). The test overrides it with a blocker that unblocks on context cancel, asserts `Start` returns immediately (non-blocking), then asserts `Stop` cancels the context and returns promptly.

```go
package daemon

import (
	"context"
	"testing"
	"time"
)

func TestProgramStartIsNonBlockingAndStopCancels(t *testing.T) {
	started := make(chan struct{})
	releasedCtx := make(chan struct{})

	p := newProgram(Options{BinaryName: "t"}.withDefaults())
	// Replace the supervisor body with a controllable blocker.
	p.run = func(ctx context.Context, _ Options) error {
		close(started)
		<-ctx.Done() // unblocks only when Stop cancels
		close(releasedCtx)
		return ctx.Err()
	}

	// Start must NOT block: it launches the supervisor in a goroutine.
	if err := p.Start(nil); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not launch the supervisor goroutine")
	}

	// Stop must cancel the context and return well within the budget.
	stopReturned := make(chan error, 1)
	go func() { stopReturned <- p.Stop(nil) }()
	select {
	case err := <-stopReturned:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return within budget")
	}
	select {
	case <-releasedCtx:
	case <-time.After(time.Second):
		t.Fatal("supervisor context was not cancelled by Stop")
	}
}

func TestProgramStopWaitsThenGivesUp(t *testing.T) {
	// A supervisor that ignores cancellation: Stop must still return (after the
	// time.After fallback fires), not hang forever.
	p := newProgram(Options{BinaryName: "t"}.withDefaults())
	p.stopTimeout = 50 * time.Millisecond // shrink the budget for the test
	p.run = func(ctx context.Context, _ Options) error {
		<-make(chan struct{}) // block forever, ignoring ctx
		return nil
	}
	_ = p.Start(nil)

	done := make(chan error, 1)
	go func() { done <- p.Stop(nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not honour its fallback timeout")
	}
}
```

- [ ] **Step 2: Run the test — expect FAIL (compile error: `newProgram`, `program`, fields undefined).**

```
go test ./pkg/daemon/ -run TestProgram -count=1
```

- [ ] **Step 3: Create `svc.go` minimal: the `osService` seam, `program` type, and `newProgram`.**

`Start(service.Service)` and `Stop(service.Service)` accept kardianos's `service.Service` so `*program` satisfies `service.Interface`. `Start` is non-blocking (goroutine); `Stop` cancels and waits on a `done` channel or the `stopTimeout` fallback (default 10s).

```go
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kardianos/service"
)

// osService is the subset of kardianos service.Service that the svc verbs use.
// Defining it as an interface lets unit tests inject a fake; the real
// service.Service satisfies it. Constructed via the newOSService seam.
type osService interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (service.Status, error)
	Run() error
}

// newOSService is the seam used to build the OS service handle. Tests override it
// to inject a fake; production uses realOSService (kardianos-backed).
var newOSService = realOSService

// program is the kardianos service body. It wraps the daemon supervisor: the OS
// service manager calls Start when the service starts and Stop on shutdown.
type program struct {
	o           Options
	run         func(ctx context.Context, o Options) error // supervisor seam (defaults to RunMonitor)
	stopTimeout time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// newProgram builds a program bound to o. o is expected to be withDefaults()'d.
func newProgram(o Options) *program {
	return &program{
		o:           o,
		run:         RunMonitor,
		stopTimeout: 10 * time.Second,
	}
}

// Start launches the supervisor in a cancelable goroutine and returns immediately,
// as required by the kardianos service.Interface contract.
func (p *program) Start(service.Service) error {
	log := p.o.logger().With(slog.String("role", "os-service"))
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := p.run(ctx, p.o); err != nil {
			log.Error("supervisor exited with error", slog.Any("err", err))
		}
	}()
	log.Info("os service started")
	return nil
}

// Stop cancels the supervisor context and waits for it to drain, up to stopTimeout,
// then returns so the OS service manager can terminate the process.
func (p *program) Stop(service.Service) error {
	log := p.o.logger().With(slog.String("role", "os-service"))
	if p.cancel != nil {
		p.cancel()
	}
	if p.done == nil {
		return nil
	}
	select {
	case <-p.done:
		log.Info("os service stopped")
	case <-time.After(p.stopTimeout):
		log.Warn("os service stop timed out; forcing exit", slog.Duration("timeout", p.stopTimeout))
	}
	return nil
}

// realOSService constructs a kardianos service.Service wrapping a program for o.
// It guards the empty ServiceName case with a friendly error before service.New
// (which would otherwise return the opaque service.ErrNameFieldRequired).
func realOSService(o Options) (osService, error) {
	if o.ServiceName == "" {
		return nil, fmt.Errorf("daemon: cannot manage OS service: ServiceName is empty (set Options.BinaryName or Options.ServiceName)")
	}
	cfg := &service.Config{
		Name:        o.ServiceName,
		DisplayName: o.ServiceName,
		Description: fmt.Sprintf("%s service", o.BinaryName),
		Arguments:   []string{"svc", "run"},
	}
	s, err := service.New(newProgram(o), cfg)
	if err != nil {
		return nil, fmt.Errorf("daemon: build OS service: %w", err)
	}
	return s, nil
}
```

- [ ] **Step 4: Run the test — expect PASS.**

```
go test ./pkg/daemon/ -run TestProgram -count=1
```

- [ ] **Step 5: Commit.**

```
git add pkg/daemon/svc.go pkg/daemon/svc_test.go
git commit -m "feat(daemon): add kardianos program wrapping the supervisor"
```

---

### Task 2: Build the `svc` cobra group with the seam-injected verbs

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/svc.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/svc_test.go` (Modify)

Each mutating verb (`install`/`uninstall`/`start`/`stop`/`restart`) has a `RunE` that calls `newOSService(o)` then the matching method. **Leave the first line of each mutating verb's `RunE` available** for C4 to prepend a `RequirePrivilege(cmd)` guard — that is, the body begins immediately with the `newOSService` call and nothing precedes it. `status` and `run` are NOT guarded.

- [ ] **Step 1: Add a failing test that injects a fake `osService` and asserts each verb dispatches to the right method.**

Append to `svc_test.go`. Note the imports grow to include `bytes`, `errors`, `github.com/kardianos/service`, `github.com/spf13/cobra` — merge them into the existing import block.

```go
import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// fakeOSService records which methods were called.
type fakeOSService struct {
	calls  []string
	status service.Status
	err    error
}

func (f *fakeOSService) Install() error   { f.calls = append(f.calls, "install"); return f.err }
func (f *fakeOSService) Uninstall() error { f.calls = append(f.calls, "uninstall"); return f.err }
func (f *fakeOSService) Start() error     { f.calls = append(f.calls, "start"); return f.err }
func (f *fakeOSService) Stop() error      { f.calls = append(f.calls, "stop"); return f.err }
func (f *fakeOSService) Restart() error   { f.calls = append(f.calls, "restart"); return f.err }
func (f *fakeOSService) Run() error       { f.calls = append(f.calls, "run"); return f.err }
func (f *fakeOSService) Status() (service.Status, error) {
	f.calls = append(f.calls, "status")
	return f.status, f.err
}

// findSub locates a direct subcommand by name.
func findSub(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q not found under %q", name, parent.Name())
	return nil
}

func withFakeOSService(t *testing.T, fake *fakeOSService) {
	t.Helper()
	prev := newOSService
	newOSService = func(Options) (osService, error) { return fake, nil }
	t.Cleanup(func() { newOSService = prev })
}

func TestSvcVerbsDispatchToOSService(t *testing.T) {
	cases := []struct{ verb, want string }{
		{"install", "install"},
		{"uninstall", "uninstall"},
		{"start", "start"},
		{"stop", "stop"},
		{"restart", "restart"},
		{"status", "status"},
		{"run", "run"},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			fake := &fakeOSService{status: service.StatusRunning}
			withFakeOSService(t, fake)

			grp := svcCommand(Options{BinaryName: "t"}.withDefaults())
			sub := findSub(t, grp, tc.verb)
			sub.SetContext(context.Background())
			var out bytes.Buffer
			sub.SetOut(&out)
			sub.SetErr(&out)
			if err := sub.RunE(sub, nil); err != nil {
				t.Fatalf("verb %q RunE: %v", tc.verb, err)
			}
			if len(fake.calls) != 1 || fake.calls[0] != tc.want {
				t.Fatalf("verb %q called %v, want [%s]", tc.verb, fake.calls, tc.want)
			}
		})
	}
}

func TestSvcRunIsHidden(t *testing.T) {
	grp := svcCommand(Options{BinaryName: "t"}.withDefaults())
	run := findSub(t, grp, "run")
	if !run.Hidden {
		t.Fatal("svc run must be Hidden")
	}
}

func TestSvcVerbPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	fake := &fakeOSService{err: sentinel}
	withFakeOSService(t, fake)

	grp := svcCommand(Options{BinaryName: "t"}.withDefaults())
	install := findSub(t, grp, "install")
	install.SetContext(context.Background())
	var out bytes.Buffer
	install.SetOut(&out)
	install.SetErr(&out)
	if err := install.RunE(install, nil); !errors.Is(err, sentinel) {
		t.Fatalf("install RunE error = %v, want wraps %v", err, sentinel)
	}
}
```

- [ ] **Step 2: Run the test — expect FAIL (compile error: `svcCommand` undefined).**

```
go test ./pkg/daemon/ -run TestSvc -count=1
```

- [ ] **Step 3: Append the `svc` cobra group to `svc.go`.**

Add `github.com/spf13/cobra` to the `svc.go` import block (merge, do not add a second block). Each mutating verb begins its `RunE` directly with `newOSService(o)` — no statement precedes it, leaving the slot for C4's `RequirePrivilege(cmd)`. `status` maps kardianos status to friendly text; `run` is `Hidden: true` and calls `Run()`.

```go
// svcCommand builds the `svc` group: the OS-service (kardianos) lifecycle. o is
// expected to be withDefaults()'d. The mutating verbs (install/uninstall/start/
// stop/restart) begin their RunE with newOSService so C4 can prepend a
// RequirePrivilege(cmd) guard as the first statement.
func svcCommand(o Options) *cobra.Command {
	svc := &cobra.Command{
		Use:   "svc",
		Short: fmt.Sprintf("Manage the %s OS service (install/start/stop/...)", o.BinaryName),
	}
	svc.AddCommand(
		svcInstallCommand(o),
		svcUninstallCommand(o),
		svcStartCommand(o),
		svcStopCommand(o),
		svcRestartCommand(o),
		svcStatusCommand(o),
		svcRunCommand(o),
	)
	return svc
}

func svcInstallCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register the service with the OS init system (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			if err := s.Install(); err != nil {
				return fmt.Errorf("svc install: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "installed")
			return nil
		},
	}
}

func svcUninstallCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the service from the OS init system (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			if err := s.Uninstall(); err != nil {
				return fmt.Errorf("svc uninstall: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "uninstalled")
			return nil
		},
	}
}

func svcStartCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Ask the OS init system to start the service (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			if err := s.Start(); err != nil {
				return fmt.Errorf("svc start: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "started")
			return nil
		},
	}
}

func svcStopCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Ask the OS init system to stop the service (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			if err := s.Stop(); err != nil {
				return fmt.Errorf("svc stop: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			return nil
		},
	}
}

func svcRestartCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Ask the OS init system to restart the service (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			if err := s.Restart(); err != nil {
				return fmt.Errorf("svc restart: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "restarted")
			return nil
		},
	}
}

func svcStatusCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Query the OS service status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			st, err := s.Status()
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "not installed")
				return nil
			}
			switch st {
			case service.StatusRunning:
				fmt.Fprintln(cmd.OutOrStdout(), "running")
			case service.StatusStopped:
				fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			default:
				fmt.Fprintln(cmd.OutOrStdout(), "unknown")
			}
			return nil
		},
	}
}

func svcRunCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run as an OS service (invoked by the service manager)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newOSService(o)
			if err != nil {
				return err
			}
			return s.Run()
		},
	}
}
```

> Final `svc.go` import set after this step: `context`, `fmt`, `log/slog`, `time`, `github.com/kardianos/service`, `github.com/spf13/cobra`.

- [ ] **Step 4: Run the test — expect PASS.**

```
go test ./pkg/daemon/ -run TestSvc -count=1
```

- [ ] **Step 5: Commit.**

```
git add pkg/daemon/svc.go pkg/daemon/svc_test.go
git commit -m "feat(daemon): add svc group for OS-managed service lifecycle"
```

---

### Task 3: Wire the `svc` group into `AttachCommands` (preserving existing wiring)

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/cobra.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/cobra_test.go` (Modify)

- [ ] **Step 1: Add a failing test requiring `svc` (top-level, visible) and its subcommands, alongside the untouched `service`/`__monitor`/`__worker`.**

Append this to `cobra_test.go`. The existing `TestAttachRegistersServiceAndHiddenCommands` stays as-is and must keep passing — proof the `service` wiring is untouched.

```go
func TestAttachRegistersSvcGroup(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	if err := AttachCommands(root, Options{
		BinaryName: "t",
		Serve:      func(context.Context, Ports) error { return nil },
	}); err != nil {
		t.Fatalf("AttachCommands: %v", err)
	}

	var svc *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "svc" {
			svc = c
		}
	}
	if svc == nil {
		t.Fatal("svc group not registered by AttachCommands")
	}
	if svc.Hidden {
		t.Fatal("svc group must be visible")
	}

	want := map[string]bool{ // name -> hidden
		"install": false, "uninstall": false, "start": false,
		"stop": false, "restart": false, "status": false, "run": true,
	}
	got := map[string]bool{}
	for _, c := range svc.Commands() {
		got[c.Name()] = c.Hidden
	}
	for name, hidden := range want {
		h, ok := got[name]
		if !ok {
			t.Fatalf("svc subcommand %q not registered", name)
		}
		if h != hidden {
			t.Fatalf("svc %q hidden=%v, want %v", name, h, hidden)
		}
	}
}
```

- [ ] **Step 2: Run the test — expect FAIL (svc not registered).**

```
go test ./pkg/daemon/ -run TestAttach -count=1
```

- [ ] **Step 3: Add the `svc` group to `AttachCommands` in `cobra.go` with a one-line edit. Do NOT touch the `service`/`monitor`/`worker` construction.**

Change the existing registration line (currently `root.AddCommand(service, monitor, worker)`):

```go
	root.AddCommand(service, monitor, worker, svcCommand(o))
	return nil
```

That single edit is the entire production change; `service`, `monitor`, `worker` and their subcommands are unchanged. `o` is the already-`withDefaults()`'d Options already in scope in `AttachCommands`.

- [ ] **Step 4: Run the test — expect PASS. Then run the whole package to prove existing tests (including `TestAttachRegistersServiceAndHiddenCommands`) still pass.**

```
go test ./pkg/daemon/ -run TestAttach -count=1
go test ./pkg/daemon/ -count=1
```

- [ ] **Step 5: Commit.**

```
git add pkg/daemon/cobra.go pkg/daemon/cobra_test.go
git commit -m "feat(daemon): register svc group in AttachCommands"
```

---

### Task 4: Add a minimal `ExitCodeFor` mapper to `pkg/daemon`

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/exitstatus.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/exitstatus_test.go` (Create)

The demo `Execute()` (Task 5) maps errors through `daemon.ExitCodeFor`. C5 provides a minimal version: `nil` -> `ExitSuccess` (0), any non-nil error -> `ExitError` (1). **C4 later EXTENDS this function** (adds the `ErrNeedsPrivilege -> ExitNeedsPrivilege(5)` branch) and adds `ExitNeedsPrivilege=5` to this file — C5 does NOT define code 5 and does NOT reuse 0/1/3/4 for anything new.

- [ ] **Step 1: Add a failing test for the minimal mapping.**

Create `exitstatus_test.go`:

```go
package daemon

import (
	"errors"
	"testing"
)

func TestExitCodeForNilIsSuccess(t *testing.T) {
	if got := ExitCodeFor(nil); got != ExitSuccess.AsInt() {
		t.Fatalf("ExitCodeFor(nil) = %d, want %d", got, ExitSuccess.AsInt())
	}
}

func TestExitCodeForGenericErrorIsExitError(t *testing.T) {
	if got := ExitCodeFor(errors.New("boom")); got != ExitError.AsInt() {
		t.Fatalf("ExitCodeFor(err) = %d, want %d", got, ExitError.AsInt())
	}
}
```

- [ ] **Step 2: Run the test — expect FAIL (`ExitCodeFor` undefined).**

```
go test ./pkg/daemon/ -run TestExitCodeFor -count=1
```

- [ ] **Step 3: Append `ExitCodeFor` to `exitstatus.go`.**

```go
// ExitCodeFor maps an error returned from command execution to a process exit code.
// nil -> ExitSuccess (0); any other error -> ExitError (1). C4 extends this with the
// ErrNeedsPrivilege -> ExitNeedsPrivilege (5) branch.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitSuccess.AsInt()
	}
	return ExitError.AsInt()
}
```

- [ ] **Step 4: Run the test — expect PASS.**

```
go test ./pkg/daemon/ -run TestExitCodeFor -count=1
```

- [ ] **Step 5: Commit.**

```
git add pkg/daemon/exitstatus.go pkg/daemon/exitstatus_test.go
git commit -m "feat(daemon): add ExitCodeFor error-to-exit-code mapper"
```

---

### Task 5: Refactor the `cmd/daemon` demo binary onto `AttachCommands`

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/cmd/daemon/daemon.go`
- Delete: `D:/weaver-sync/modules/daemon/cmd/daemon/cmd_service.go`
- Delete: `D:/weaver-sync/modules/daemon/internal/service/service.go` (dead once the demo no longer imports it)

The demo provides a minimal real `serve` (blocks on `ctx.Done()`, logs `"serving"`) and calls `daemon.AttachCommands(rootCmd, daemon.Options{...})` so it inherits `service`, `svc`, `__monitor`, `__worker`. `Execute()` maps errors through `daemon.ExitCodeFor`. `cmd_version.go`, `cmd_cmdtree.go`, `cmd_aicontext.go` are untouched (they only reference `rootCmd`).

- [ ] **Step 1: Delete the hand-rolled kardianos stub and the dead internal handler.**

```
git rm cmd/daemon/cmd_service.go internal/service/service.go
```

(Leave `internal/parameters/` intact — `daemon.go`/config still use it. If `internal/service/` becomes an empty directory, that is harmless.)

- [ ] **Step 2: Replace the entire contents of `cmd/daemon/daemon.go`.**

The new file drops the `service`/`run` hardcoding, provides `serve`, calls `daemon.AttachCommands`, and routes `Execute()` through `daemon.ExitCodeFor`.

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/inovacc/daemon/internal/parameters"
	"github.com/inovacc/daemon/pkg/daemon"

	"github.com/inovacc/config"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "daemon",
	Short: "daemon is a CLI application",
	Long: `daemon is a CLI application

This is a CLI application built with Cobra.`,
}

// serve is the demo worker body: it blocks until the context is cancelled so the
// monitor→worker supervision chain (and the OS service via svc run) has something
// real to run.
func serve(ctx context.Context, p daemon.Ports) error {
	slog.Info("serving", slog.Int("http_port", p.HTTP), slog.Int("grpc_port", p.GRPC))
	<-ctx.Done()
	slog.Info("stopped serving")
	return nil
}

// Execute runs the root command and maps any error to a process exit code via
// daemon.ExitCodeFor (so svc privilege failures from C4 surface as exit 5 rather
// than a generic 1).
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(daemon.ExitCodeFor(err))
	}
}

func main() {
	Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.Version = GetVersionJSON()
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "config.yaml", "config file (default is config.yaml)")

	if err := daemon.AttachCommands(rootCmd, daemon.Options{
		BinaryName: "daemon",
		Serve:      serve,
	}); err != nil {
		cobra.CheckErr(err)
	}
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile == "" {
		_, _ = fmt.Fprint(os.Stdout, "Using default config file: config.yaml")
	}

	// Load configuration from a file, applying defaults if needed
	if err := config.InitServiceConfig(&parameters.Service{}, cfgFile); err != nil {
		_, _ = fmt.Fprint(os.Stdout, "failed to load config: %w", err)
	}
}
```

- [ ] **Step 3: Build the demo binary — expect SUCCESS (no remaining reference to `internal/service` or `serviceProgram`/`newService`).**

```
go build ./cmd/daemon/
```

- [ ] **Step 4: Smoke-check the wired commands (build/help check, not a service install).**

```
go run ./cmd/daemon/ --help
go run ./cmd/daemon/ svc --help
```

Expect top-level help to show `service` and `svc` (not `__monitor`/`__worker`), and `svc --help` to list `install/uninstall/start/stop/restart/status` with `run` hidden.

- [ ] **Step 5: Run the whole module test suite — expect PASS.**

```
go test ./... -count=1
```

- [ ] **Step 6: Commit.**

```
git add -A
git commit -m "refactor(daemon): wire demo binary onto AttachCommands and ExitCodeFor"
```

---

### Task 6: Final verification (build, vet, cross-compile, race, mod drift, purity)

**Files:** none (verification only). Run from the daemon module root `D:/weaver-sync/modules/daemon` unless noted. Use PowerShell on this Windows host. Each command must exit 0 unless stated otherwise.

- [ ] **Step 1: Build and vet the whole module.**

```
go build ./...
go vet ./...
```

- [ ] **Step 2: Cross-compile for every target OS at amd64, AND vet under each (kardianos syscalls are build-verified this way).**

```powershell
foreach ($os in 'windows','linux','darwin') {
  $env:GOOS = $os; $env:GOARCH = 'amd64'
  Write-Host "== build $os/amd64 =="; go build ./...;  if ($LASTEXITCODE -ne 0) { throw "build $os failed" }
  Write-Host "== vet   $os/amd64 =="; go vet ./...;    if ($LASTEXITCODE -ne 0) { throw "vet $os failed" }
}
Remove-Item Env:GOOS; Remove-Item Env:GOARCH
```

Every build and vet must exit 0.

- [ ] **Step 3: Race-enabled test run.**

```
go test ./... -race -count=1
```

- [ ] **Step 4: Purity check — the logger module's `pkg/logger` and `pkg/obsv` must import ZERO kardianos. Run from any shell; uses `Get-ChildItem -Recurse -Filter *.go` piped to `Select-String -SimpleMatch` (NOT a `**` glob).**

```powershell
$hits = Get-ChildItem -Path 'D:/weaver-sync/modules/logger/pkg/logger','D:/weaver-sync/modules/logger/pkg/obsv' -Recurse -Filter *.go |
  Select-String -SimpleMatch 'kardianos'
if ($hits) {
  $hits | ForEach-Object { Write-Host $_.Path ':' $_.Line }
  throw 'PURITY VIOLATION: kardianos imported in pkg/logger or pkg/obsv'
} else {
  Write-Host 'PURITY OK: zero kardianos references in pkg/logger and pkg/obsv'
}
```

Expect `PURITY OK`. (If either directory does not exist yet in the logger module, the command yields no hits and still prints `PURITY OK`.)

- [ ] **Step 5: Module hygiene — `go mod tidy` must produce no drift; kardianos stays pinned at v1.2.2, x/sys at v0.34.0 (NEVER `@latest`).**

```
go mod tidy
git diff --exit-code go.mod go.sum
```

`git diff --exit-code` must exit 0 (no changes — no new dependency was introduced; kardianos and x/sys were already required). Then confirm the pins explicitly:

```powershell
Select-String -Path 'D:/weaver-sync/modules/daemon/go.mod' -SimpleMatch 'kardianos/service v1.2.2'
Select-String -Path 'D:/weaver-sync/modules/daemon/go.mod' -SimpleMatch 'golang.org/x/sys v0.34.0'
```

Both lines must be found unchanged.

- [ ] **Step 6: Final full test run for the record.**

```
go test ./... -count=1
```

Must exit 0. Verification only — nothing to commit unless `go mod tidy` produced a change (it must not).

---

## Self-Review

- **C2 `service` group untouched:** The locked C2 daemonize verbs (`service` / `service start` / `service stop` / `service status` in `cobra.go`, backed by `Start`/`Stop` in `daemonize.go` + `serverinfo` PID file) are not renamed or repurposed. Task 3 makes a single additive edit to `AttachCommands` (`root.AddCommand(service, monitor, worker, svcCommand(o))`); the `service`, `monitor`, and `worker` constructions are byte-for-byte unchanged, and the existing `TestAttachRegistersServiceAndHiddenCommands` test must keep passing as proof.
- **New `svc` group is the OS-service (kardianos):** Verbs `install/uninstall/start/restart/stop` (privileged), `status` (unprivileged), `run` (`Hidden:true`, the OS-manager entry that runs the REAL supervisor via `program.Start` → `RunMonitor`). `service.Config.Arguments` is `["svc","run"]`, matching the hidden entry. `service.Config.Name` is `o.ServiceName` (which `withDefaults()` already defaults to `BinaryName`); the empty-name case is guarded with a friendly wrapped error in `realOSService` BEFORE `service.New`, so users never see the raw `service.ErrNameFieldRequired`.
- **Program wraps the supervisor:** `program.Start` launches `RunMonitor(ctx, o)` in a goroutine with a cancelable context and returns immediately (kardianos contract); `program.Stop` cancels and waits on a `done` channel or a `time.After(10*time.Second)` fallback, so it never hangs even if the supervisor ignores cancellation (covered by `TestProgramStopWaitsThenGivesUp`, which shrinks the timeout for speed).
- **Testable seam:** The unexported `osService` interface (Install/Uninstall/Start/Stop/Restart/Status/Run) is satisfied by kardianos's `service.Service` and constructed through the `newOSService = realOSService` package-var seam. Unit tests inject `fakeOSService` and assert each verb dispatches to exactly the right method (`TestSvcVerbsDispatchToOSService`) and propagates errors (`TestSvcVerbPropagatesError`). kardianos itself is only build-verified (cross-compile + vet across windows/linux/darwin in Task 6).
- **Purity preserved:** kardianos now imports into `pkg/daemon` (allowed — the daemon module may use it). The logger module's `pkg/logger` and `pkg/obsv` import zero kardianos; Task 6 Step 4 verifies this with `Get-ChildItem -Recurse -Filter *.go | Select-String -SimpleMatch 'kardianos'` (no `**` glob). `pkg/bootstrap` importing `daemon` → kardianos transitively is fine and out of scope here.
- **Demo refactor (item 7):** `cmd/daemon` drops the hand-rolled `serviceProgram`/`newService`/`serviceCmd` stub and stops importing `internal/service` (the dead `internal/service/service.go` Handler is deleted). It provides a minimal real `serve` (logs `"serving"`, blocks on `ctx.Done()`) and calls `daemon.AttachCommands(rootCmd, daemon.Options{BinaryName:"daemon", Serve: serve})`. `Execute()` now maps errors via `os.Exit(daemon.ExitCodeFor(err))` instead of `cobra.CheckErr`, so once C4 lands, `svc` privilege failures exit 5. `cmd_version.go`/`cmd_cmdtree.go`/`cmd_aicontext.go` are untouched (they only reference `rootCmd`).
- **C4 hand-off (explicitly NOT implemented here):** C5 does NOT add privilege gating. Each mutating verb's `RunE` begins directly with `newOSService(o)` so **C4 prepends `RequirePrivilege(cmd)` as the literal first statement** of `svc install/uninstall/start/stop/restart` (NOT `status`, NOT `run`). C5 ships a *minimal* `ExitCodeFor` (nil→0, else→1) and does NOT define `ExitNeedsPrivilege`; C4 extends `exitstatus.go` with `ExitNeedsPrivilege=5` and adds the `ErrNeedsPrivilege→5` branch to `ExitCodeFor`. Because the demo's `Execute()` already routes through `daemon.ExitCodeFor`, no further demo change is needed when C4 lands — the exit-5 path activates automatically.
- **C3 note:** The `ExitUpgrade` re-exec is C3's concern and is untouched here; `monitor.go`'s `ExitUpgrade` case still restarts as before.
- **No new dependencies / no drift:** kardianos v1.2.2 and x/sys v0.34.0 are already direct requires in `go.mod`; no module is added or bumped. Task 6 proves `go mod tidy` yields no diff and the pins are intact (never `@latest`).
- **Conventional commits, zero AI attribution:** Every commit message is a conventional `feat`/`refactor` line with no `Co-Authored-By` and no AI attribution.
