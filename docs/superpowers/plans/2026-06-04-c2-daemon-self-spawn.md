# Sub-project C2 — Daemon Self-Spawn / Daemonize

> Part of sub-project C. Target module: **github.com/inovacc/daemon** at `D:\weaver-sync\modules\daemon`. Builds on C1 (lifecycle logging). Decision: detect+guide elevation (C4), but C2 just backgrounds the daemon.

**Goal:** `service start` spawns a **detached background monitor**; `service stop` kills the process tree. Guard against recursive daemonization via a `<BINARY>_DAEMON_CHILD` env var. Orchestration is unit-tested via an injectable spawn seam; the platform syscalls are build-verified across `GOOS`.

**Files (new in `pkg/daemon/`):** `daemonize.go` (Start/Stop/env-guard, platform-agnostic), `spawn_windows.go` + `spawn_unix.go` (build-tagged `spawnDetached`), `stop_windows.go` + `stop_unix.go` (build-tagged `stopProcess`), `daemonize_test.go`. Modify `cobra.go` (add `start`/`stop` subcommands). Go 1.25.

**Verification note:** the real `spawnDetached`/`stopProcess` bodies (detached process creation, `taskkill`/`SIGTERM`) can't be runtime-tested on this Windows box — they are **build-verified across `GOOS=windows/linux/darwin`** and exercised in tests via an injectable seam. Behavior is per well-established Go patterns.

---

### Task 1: Platform-agnostic Start/Stop + env guard (`daemonize.go`)

- [ ] **Create `pkg/daemon/daemonize.go`:**
```go
package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/inovacc/daemon/pkg/serverinfo"
)

// ErrAlreadyRunning is returned by Start when a live instance already exists.
var ErrAlreadyRunning = errors.New("daemon: already running")

// ErrNotRunning is returned by Stop when no live instance exists.
var ErrNotRunning = errors.New("daemon: not running")

// spawnDetachedFn / stopProcessFn are seams overridden in tests; production points at
// the platform implementations (spawn_*.go / stop_*.go).
var (
	spawnDetachedFn = spawnDetached
	stopProcessFn   = stopProcess
)

// childEnvName is the recursion-guard env var, e.g. "MY_APP" -> "MY_APP_DAEMON_CHILD".
func childEnvName(binaryName string) string {
	up := strings.ToUpper(binaryName)
	up = strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, up)
	return up + "_DAEMON_CHILD"
}

// Start daemonizes: it spawns a detached __monitor process and returns its pid.
// It returns ErrAlreadyRunning (with the live pid) when an instance is already up,
// and refuses to daemonize from within a daemon child (guarded by the env var).
func Start(o Options) (int, error) {
	o = o.withDefaults()
	guard := childEnvName(o.BinaryName)
	if os.Getenv(guard) != "" {
		return 0, errors.New("daemon: refusing to daemonize from within a daemon child")
	}
	store := serverinfo.NewStore(o.DataDir)
	if info := store.IsRunning(); info != nil {
		return info.PID, ErrAlreadyRunning
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	env := append(os.Environ(), guard+"=1")
	pid, err := spawnDetachedFn(exe, o.buildMonitorArgs(), env)
	if err != nil {
		return 0, fmt.Errorf("daemon: spawn: %w", err)
	}
	// TOCTOU health wait: the monitor writes serverinfo on startup.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if store.IsRunning() != nil {
			return pid, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pid, nil // spawned; serverinfo not yet observed — caller may re-check via status
}

// Stop reads the serverinfo (monitor) pid and terminates the daemon process tree.
func Stop(o Options) error {
	o = o.withDefaults()
	store := serverinfo.NewStore(o.DataDir)
	info := store.IsRunning()
	if info == nil {
		return ErrNotRunning
	}
	if err := stopProcessFn(info.PID); err != nil {
		return fmt.Errorf("daemon: stop pid %d: %w", info.PID, err)
	}
	_ = store.Remove()
	return nil
}
```

### Task 2: Platform spawn-detached (`spawn_windows.go` / `spawn_unix.go`)

- [ ] **Create `pkg/daemon/spawn_windows.go`:**
```go
//go:build windows

package daemon

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// spawnDetached starts exe fully detached: no console window, its own process group,
// not tied to the parent's lifetime. Returns the child pid.
func spawnDetached(exe string, args, env []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
```

- [ ] **Create `pkg/daemon/spawn_unix.go`:**
```go
//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// spawnDetached starts exe in a new session (setsid), detached from the controlling
// terminal, with stdio discarded. Returns the child pid.
func spawnDetached(exe string, args, env []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
```

### Task 3: Platform stop (`stop_windows.go` / `stop_unix.go`)

- [ ] **Create `pkg/daemon/stop_windows.go`:**
```go
//go:build windows

package daemon

import (
	"os/exec"
	"strconv"
)

// stopProcess kills the process tree rooted at pid (taskkill /T kills children too).
func stopProcess(pid int) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
```

- [ ] **Create `pkg/daemon/stop_unix.go`:**
```go
//go:build !windows

package daemon

import "syscall"

// stopProcess terminates the daemon. The monitor is a session leader (Setsid), so a
// negative pid signals the whole process group (monitor + worker); on failure it
// falls back to signalling the single process.
func stopProcess(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
```

### Task 4: Wire `start` / `stop` commands (`cobra.go`)

- [ ] **In `AttachCommands` (cobra.go),** change `service.AddCommand(statusCommand(o))` to:
```go
	service.AddCommand(statusCommand(o), startCommand(o), stopCommand(o))
```
and add (cobra.go needs `"errors"` already imported — it is):
```go
func startCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, err := Start(o)
			if errors.Is(err, ErrAlreadyRunning) {
				fmt.Fprintf(cmd.OutOrStdout(), "already running: pid=%d\n", pid)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started: pid=%d\n", pid)
			return nil
		},
	}
}

func stopCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := Stop(o); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			return nil
		},
	}
}
```

### Task 5: Tests (`daemonize_test.go`) — orchestration via the injectable seam

```go
package daemon

import (
	"errors"
	"os"
	"testing"

	"github.com/inovacc/daemon/pkg/serverinfo"
)

func TestChildEnvName(t *testing.T) {
	if got := childEnvName("logger-demo"); got != "LOGGER_DEMO_DAEMON_CHILD" {
		t.Errorf("childEnvName = %q", got)
	}
}

func TestStartRefusesFromChild(t *testing.T) {
	o := Options{BinaryName: "t", DataDir: t.TempDir()}
	t.Setenv(childEnvName("t"), "1")
	if _, err := Start(o); err == nil {
		t.Error("Start must refuse from within a daemon child")
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	// Write a serverinfo whose PID is THIS test process (guaranteed alive).
	_ = serverinfo.NewStore(dir).Write(serverinfo.Info{PID: os.Getpid()})
	pid, err := Start(Options{BinaryName: "t", DataDir: dir})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestStartSpawnsAndWaits(t *testing.T) {
	dir := t.TempDir()
	store := serverinfo.NewStore(dir)
	orig := spawnDetachedFn
	t.Cleanup(func() { spawnDetachedFn = orig })
	// Fake spawn: simulate the monitor by writing serverinfo, return a fake pid.
	spawnDetachedFn = func(exe string, args, env []string) (int, error) {
		// the guard env must be present in the child env
		found := false
		for _, e := range env {
			if e == childEnvName("t")+"=1" {
				found = true
			}
		}
		if !found {
			t.Error("child env guard not set on spawn")
		}
		_ = store.Write(serverinfo.Info{PID: os.Getpid()})
		return 4242, nil
	}
	pid, err := Start(Options{BinaryName: "t", DataDir: dir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if pid != 4242 {
		t.Errorf("pid = %d, want 4242", pid)
	}
}

func TestStopNotRunning(t *testing.T) {
	if err := Stop(Options{BinaryName: "t", DataDir: t.TempDir()}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
}

func TestStopCallsPlatformStop(t *testing.T) {
	dir := t.TempDir()
	_ = serverinfo.NewStore(dir).Write(serverinfo.Info{PID: os.Getpid()})
	orig := stopProcessFn
	t.Cleanup(func() { stopProcessFn = orig })
	called := 0
	stopProcessFn = func(pid int) error { called = pid; return nil }
	if err := Stop(Options{BinaryName: "t", DataDir: dir}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if called != os.Getpid() {
		t.Errorf("stopProcess called with %d, want %d", called, os.Getpid())
	}
	// serverinfo should be removed after stop
	if serverinfo.NewStore(dir).IsRunning() != nil {
		t.Error("serverinfo not removed after Stop")
	}
}
```

### Verify & commit
- [ ] `go build ./...`; **cross-compile all platforms:** `GOOS=windows go build ./...`, `GOOS=linux go build ./...`, `GOOS=darwin go build ./...` (all exit 0 — proves both `spawn_*`/`stop_*` files compile).
- [ ] `go vet ./...`; `go test ./... -race -count=1` (all PASS incl. existing supervisor tests + the new daemonize tests).
- [ ] Commit: `git add -A && git commit -m "feat(daemon): self-spawn/daemonize — service start/stop, detached spawn, env guard"` (in `D:\weaver-sync\modules\daemon`).

### Notes
- The real detached-spawn / taskkill / setsid behavior is build-verified, not runtime-tested here; orchestration (env guard, already-running, health-wait, stop-removes-serverinfo) IS unit-tested via the seam.
- `service` (bare) stays foreground for debugging; `service start` daemonizes; `service stop` kills the tree; `service status` (existing) reports.
- Re-exec for binary upgrade is **C3**; persistent OS-service install is **C5**.
