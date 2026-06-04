# C3 Self-Update (Binary Upgrade Re-exec) Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox syntax.

**Goal:** When the worker exits with `ExitUpgrade` (4), make the monitor re-exec the (new) binary image — `syscall.Exec` in place on Unix, a fresh detached child + `os.Exit(0)` on Windows — instead of merely restarting the worker, with a safe fallback to the existing restart behavior if re-exec fails.

**Architecture:** Two new platform-split files (`reexec_unix.go` / `reexec_windows.go`) provide `reexecSelf(args []string) error`, hidden behind a package-var seam `reexecFn = reexecSelf` that mirrors the existing `spawnDetachedFn` / `stopProcessFn` pattern. The monitor's `ExitUpgrade` case calls `reexecFn(m.o.buildMonitorArgs())`; the platform syscalls are build-verified by cross-compile while the orchestration (the ExitUpgrade branch) is unit-tested by stubbing the seam. A failed re-exec degrades gracefully to a restart so a self-update error never kills the running service.

**Tech Stack:** Go 1.25; `syscall` (Unix `Exec`), `os/exec` + `golang.org/x/sys/windows` (already a direct dep at v0.34.0, so no new module); `log/slog` for lifecycle logs; standard `testing` with package-internal seam injection.

---

### Task 1: Add the `reexecFn` package-var seam and Unix implementation

The seam must exist and compile on every platform before the monitor can reference it. `reexecSelf` itself is platform-specific, so the seam declaration lives in the platform-neutral monitor file alongside the existing struct, but the *function it points at* is defined per-platform. We add the Unix file first (it is the documented in-place replace) and the seam variable.

**Files:**
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/reexec_unix.go`
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/monitor.go` (add the `reexecFn` package var)
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/reexec_test.go`

- [ ] **Step 1: Write a failing test that the seam defaults to the real implementation.**
  This test compiles on all platforms (it only checks the seam is wired, it never calls it). Create `reexec_test.go`:
  ```go
  package daemon

  import (
  	"reflect"
  	"testing"
  )

  // TestReexecFnDefaultsToReal asserts the seam is wired to the real platform
  // implementation by default, mirroring spawnDetachedFn / stopProcessFn.
  func TestReexecFnDefaultsToReal(t *testing.T) {
  	if reexecFn == nil {
  		t.Fatal("reexecFn must default to a non-nil implementation")
  	}
  	want := reflect.ValueOf(reexecSelf).Pointer()
  	got := reflect.ValueOf(reexecFn).Pointer()
  	if got != want {
  		t.Fatalf("reexecFn must default to reexecSelf")
  	}
  }
  ```

- [ ] **Step 2: Run the test — expect FAIL (compile error: `reexecFn` and `reexecSelf` undefined).**
  ```
  go test ./pkg/daemon/ -run TestReexecFnDefaultsToReal
  ```
  Expected: build failure — `undefined: reexecFn`, `undefined: reexecSelf`.

- [ ] **Step 3: Add the `reexecFn` package-var seam to `monitor.go`.**
  Insert this block immediately after the `monitor` struct definition (after its closing `}` at line 26), so the seam sits beside the other monitor wiring:
  ```go
  // reexecFn is a seam (mirrors spawnDetachedFn / stopProcessFn) overridden in tests;
  // production points at the platform implementation in reexec_unix.go / reexec_windows.go.
  // On success it does not return (the process image is replaced or exits).
  var reexecFn = reexecSelf
  ```

- [ ] **Step 4: Create the Unix implementation `reexec_unix.go`.**
  Verified by spike: `syscall.Exec(self, argv, os.Environ())` is the documented in-place image replace and compiles for `GOOS=linux` and `GOOS=darwin`.
  ```go
  //go:build !windows

  package daemon

  import (
  	"fmt"
  	"os"
  	"syscall"
  )

  // reexecSelf replaces the current process image in place with a fresh exec of the
  // same binary on disk, forwarding args. On success it does NOT return — the running
  // image is gone and the new image takes over with the same PID. It returns an error
  // only when locating the executable or the exec syscall itself fails.
  func reexecSelf(args []string) error {
  	self, err := os.Executable()
  	if err != nil {
  		return fmt.Errorf("locate executable: %w", err)
  	}
  	argv := append([]string{self}, args...)
  	return syscall.Exec(self, argv, os.Environ())
  }
  ```

- [ ] **Step 5: Run the test — expect FAIL on Windows only / PASS on Unix.**
  ```
  go test ./pkg/daemon/ -run TestReexecFnDefaultsToReal
  ```
  Expected on this Windows dev box: build failure — `undefined: reexecSelf` for the `windows` build (no `reexec_windows.go` yet). Task 2 supplies it. Do NOT commit until Task 2 makes the package compile on Windows.

- [ ] **Step 6: (Deferred commit.)** Hold the commit until Task 2; committing now would leave the Windows build broken. Note this carried-forward item and proceed to Task 2.

---

### Task 2: Add the Windows implementation and make the package compile everywhere

**Files:**
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/reexec_windows.go`

- [ ] **Step 1: Create the Windows implementation `reexec_windows.go`.**
  Verified by spike: the detached `CreationFlags` reuse the exact pattern from `spawn_windows.go` (`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW`), `cmd.Start()` then `os.Exit(0)` so the new image supersedes the old. `golang.org/x/sys/windows` is already a direct dep — no new module.
  ```go
  //go:build windows

  package daemon

  import (
  	"fmt"
  	"os"
  	"os/exec"
  	"syscall"

  	"golang.org/x/sys/windows"
  )

  // reexecSelf spawns a fresh DETACHED child running the (new) binary image as a
  // monitor, then exits the current process so the new image supersedes the old.
  // Windows has no exec(); this is the equivalent of the Unix in-place replace.
  // It reuses the detached CreationFlags from spawn_windows.go. On success it does
  // NOT return (os.Exit terminates the process); it returns an error only when
  // locating the executable or starting the child fails.
  func reexecSelf(args []string) error {
  	self, err := os.Executable()
  	if err != nil {
  		return fmt.Errorf("locate executable: %w", err)
  	}
  	cmd := exec.Command(self, args...)
  	cmd.Env = os.Environ()
  	// DETACHED_PROCESS + CREATE_NO_WINDOW drop stdio, so nil-ing it is unnecessary (matches spawn_windows.go).
  	cmd.SysProcAttr = &syscall.SysProcAttr{
  		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
  		HideWindow:    true,
  	}
  	if err := cmd.Start(); err != nil {
  		return fmt.Errorf("spawn upgraded monitor: %w", err)
  	}
  	_ = cmd.Process.Release()
  	os.Exit(0)
  	return nil
  }
  ```

- [ ] **Step 2: Run the seam test — expect PASS on all platforms now.**
  ```
  go test ./pkg/daemon/ -run TestReexecFnDefaultsToReal
  ```
  Expected: `ok  github.com/inovacc/daemon/pkg/daemon`.

- [ ] **Step 3: Confirm the package compiles on every target before committing.**
  ```
  go build ./...
  $env:GOOS="windows"; go build ./...; $env:GOOS="linux"; go build ./...; $env:GOOS="darwin"; go build ./...; Remove-Item Env:\GOOS
  ```
  Expected: every `go build` exits 0.

- [ ] **Step 4: Commit (conventional, no AI attribution).**
  ```
  git add pkg/daemon/reexec_unix.go pkg/daemon/reexec_windows.go pkg/daemon/monitor.go pkg/daemon/reexec_test.go
  git commit -m "feat(daemon): add reexecSelf platform seam for self-update"
  ```

---

### Task 3: Wire the `ExitUpgrade` branch to re-exec, with restart fallback

The current `ExitUpgrade` case (monitor.go lines 79-83) just logs and restarts the worker. Replace that with: log `"re-executing for binary upgrade"`, call `reexecFn(m.o.buildMonitorArgs())`; on a returned error, log it and fall through to the existing restart behavior so a failed re-exec never kills the service. (On success `reexecFn` does not return, so any code after it only runs on error.)

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/monitor.go` (the `ExitUpgrade` case in `run`)
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/monitor_test.go` (add two tests)

- [ ] **Step 1: Write two failing tests in `monitor_test.go`.**
  The first asserts the ExitUpgrade branch invokes `reexecFn` with the monitor args (stubbed — no real exec). The second asserts a `reexecFn` error degrades to a restart (the worker is spawned again, then exits cleanly). Both restore the seam via `t.Cleanup`. Append:
  ```go
  func TestUpgradeInvokesReexec(t *testing.T) {
  	orig := reexecFn
  	t.Cleanup(func() { reexecFn = orig })

  	var gotArgs []string
  	called := false
  	reexecFn = func(args []string) error {
  		called = true
  		gotArgs = args
  		return nil // success path returns nil here (real impl never returns)
  	}

  	// reexecFn returns nil (stubbed success) so the loop continues; the toggling
  	// closure requests upgrade on the first spawn, then exits clean on the next.
  	first := true
  	m := newMonitorForTest(t, func(context.Context, []string) int {
  		if first {
  			first = false
  			return ExitUpgrade.AsInt()
  		}
  		return ExitSuccess.AsInt()
  	})

  	if err := m.run(context.Background()); err != nil {
  		t.Fatalf("run should return nil, got %v", err)
  	}
  	if !called {
  		t.Fatal("ExitUpgrade must invoke reexecFn")
  	}
  	want := m.o.buildMonitorArgs()
  	if len(gotArgs) != len(want) {
  		t.Fatalf("reexecFn called with %v, want %v", gotArgs, want)
  	}
  	for i := range want {
  		if gotArgs[i] != want[i] {
  			t.Fatalf("reexecFn arg %d = %q, want %q", i, gotArgs[i], want[i])
  		}
  	}
  }

  func TestUpgradeReexecErrorDegradesToRestart(t *testing.T) {
  	orig := reexecFn
  	t.Cleanup(func() { reexecFn = orig })
  	reexecFn = func([]string) error {
  		return errors.New("boom") // re-exec failed; monitor must NOT die
  	}

  	calls := 0
  	m := newMonitorForTest(t, func(context.Context, []string) int {
  		calls++
  		if calls == 1 {
  			return ExitUpgrade.AsInt() // first: request upgrade (reexec fails)
  		}
  		return ExitSuccess.AsInt() // then: restart spawns worker, exits clean
  	})

  	if err := m.run(context.Background()); err != nil {
  		t.Fatalf("a failed re-exec must degrade to restart, not error: %v", err)
  	}
  	if calls != 2 {
  		t.Fatalf("expected restart after failed re-exec (2 spawns), got %d", calls)
  	}
  }
  ```

- [ ] **Step 2: Add the `errors` import and run the tests — expect FAIL.**
  Add `"errors"` to `monitor_test.go`'s imports (it currently imports `context`, `testing`, `time`); keep `time` since `newMonitorForTest` still uses it. Then:
  ```
  go test ./pkg/daemon/ -run TestUpgrade
  ```
  Expected: FAIL — the current `ExitUpgrade` case never calls `reexecFn`, so `TestUpgradeInvokesReexec` reports "ExitUpgrade must invoke reexecFn".

- [ ] **Step 3: Replace the `ExitUpgrade` case in `monitor.go`.**
  Replace the existing case (the three lines under `case ExitUpgrade:` that currently log "worker requested binary upgrade" and `continue`) with:
  ```go
  		case ExitUpgrade:
  			log.Info("worker requested binary upgrade", slog.Int("code", code.AsInt()))
  			log.Info("re-executing for binary upgrade")
  			if err := reexecFn(m.o.buildMonitorArgs()); err != nil {
  				// On Unix syscall.Exec replaces this image and never returns; on
  				// Windows reexecSelf exits the process. Reaching here means re-exec
  				// FAILED — degrade to the existing restart so we never kill the service.
  				log.Error("re-exec failed; falling back to restart", slog.Any("err", err))
  			}
  			attempt = 0
  			continue
  ```

- [ ] **Step 4: Run the tests — expect PASS.**
  ```
  go test ./pkg/daemon/ -run TestUpgrade
  ```
  Expected: `ok` — both `TestUpgradeInvokesReexec` and `TestUpgradeReexecErrorDegradesToRestart` pass.

- [ ] **Step 5: Run the full daemon test suite to confirm no regression.**
  ```
  go test ./pkg/daemon/ -count=1
  ```
  Expected: `ok` — existing monitor/lifecycle tests still pass.

- [ ] **Step 6: Commit (conventional, no AI attribution).**
  ```
  git add pkg/daemon/monitor.go pkg/daemon/monitor_test.go
  git commit -m "feat(daemon): re-exec binary on ExitUpgrade with restart fallback"
  ```

---

### Task 4: Verify — build, vet, cross-compile, race tests, purity

**Files:** none (verification only).

- [ ] **Step 1: Build and vet the whole module.**
  ```
  go build ./...
  go vet ./...
  ```
  Expected: both exit 0, no diagnostics.

- [ ] **Step 2: Cross-compile for all three targets (each must exit 0).**
  ```
  $env:GOOS="windows"; go build ./...; "windows exit=$LASTEXITCODE"
  $env:GOOS="linux";   go build ./...; "linux exit=$LASTEXITCODE"
  $env:GOOS="darwin";  go build ./...; "darwin exit=$LASTEXITCODE"
  Remove-Item Env:\GOOS
  ```
  Expected: `windows exit=0`, `linux exit=0`, `darwin exit=0`. (Spike-confirmed for the exact `reexec_*.go` source.)

- [ ] **Step 3: Run the full test suite with the race detector.**
  ```
  go test ./... -race -count=1
  ```
  Expected: all packages `ok`.

- [ ] **Step 4: Dependency hygiene — no new module was added.**
  This sub-project introduces NO new third-party dependency: `golang.org/x/sys v0.34.0` is already a direct require in `go.mod`. Still run tidy to confirm nothing drifted:
  ```
  go mod tidy
  git diff --exit-code go.mod go.sum
  ```
  Expected: `go mod tidy` makes no change; `git diff --exit-code` exits 0. `golang.org/x/sys` remains pinned at `v0.34.0`. If a future change ever needs a new dep, pin it explicitly (e.g. `go get golang.org/x/sys@v0.34.0`) and never let it appear as an unpinned `latest`.

- [ ] **Step 5: Daemon-module purity reminder (manual check).**
  The new code (`reexec_unix.go`, `reexec_windows.go`, the `reexecFn` seam, the `ExitUpgrade` change) lives ENTIRELY in `github.com/inovacc/daemon/pkg/daemon`. The Windows file's `golang.org/x/sys/windows` import and the Unix file's `syscall.Exec` MUST NOT leak into the logger module's `pkg/logger` or `pkg/obsv`. Confirm nothing in the logger module imports the daemon platform internals:
  ```
  go list -deps ./pkg/daemon/ | Select-String "golang.org/x/sys"   # expected: present (daemon only)
  ```
  Run from the logger module root, confirm `pkg/logger` and `pkg/obsv` do not depend on the daemon module at all.

- [ ] **Step 6: Final commit only if verification produced fixups; otherwise nothing to commit.**
  If any step above required a code change, commit it with a conventional message (no AI attribution), e.g.:
  ```
  git commit -am "chore(daemon): verification fixups for self-update"
  ```

---

## Self-Review

**Decision → delivering task:**
- Platform-split `reexecSelf` (Unix `syscall.Exec` in place; Windows detached spawn + `os.Exit(0)`) via build tags `//go:build !windows` / `//go:build windows` → Task 1 (Unix) + Task 2 (Windows).
- Injectable `reexecFn = reexecSelf` seam mirroring `spawnDetachedFn` / `stopProcessFn`, so the orchestration is unit-tested without a real exec → Task 1 Step 3; platform files themselves are build-verified by cross-compile → Task 4 Step 2.
- `ExitUpgrade` case calls `reexecFn(m.o.buildMonitorArgs())`, logging `"re-executing for binary upgrade"` first, replacing the "for now restart" comment → Task 3 Step 3.
- On `reexecFn` error, fall back to the existing restart (log + `continue`) so a failed re-exec never kills the service → Task 3 Step 3 + test `TestUpgradeReexecErrorDegradesToRestart` (Task 3 Step 1).
- ExitUpgrade branch invokes the seam (stubbed, no real process replacement) → test `TestUpgradeInvokesReexec` (Task 3 Step 1).
- Windows reuses the exact `CreationFlags` from `spawn_windows.go` (`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW`) → Task 2 Step 1.
- Exit-code protocol unchanged: `ExitUpgrade` stays 4; no code reused or removed → covered by leaving `exitstatus.go` untouched and Task 3 only changing behavior of the existing case.
- Optional `Options.Logger` with `slog.Default()` fallback; lifecycle logs via `o.logger()` → reused existing `log := m.o.logger()` in `run`; new logs use that same `log`.
- Import purity: the only third-party import is `golang.org/x/sys/windows`, already a direct dep, confined to the daemon module → Task 4 Steps 4-5.
- Cross-compile clean for windows/linux/darwin → Task 4 Step 2 (spike-confirmed).
- Conventional commits, no AI attribution, no Co-Authored-By → Tasks 2/3/4 commit steps.

**Carried-forward items:**
- Task 1 intentionally defers its commit (Step 6): adding `reexecFn = reexecSelf` and the Unix file breaks the Windows build until Task 2 adds `reexec_windows.go`. The first commit (Task 2 Step 4) therefore includes Task 1's files. Do not commit between Task 1 and Task 2.
- `reexecSelf` success path never returns; the post-call code in the `ExitUpgrade` case (`attempt = 0; continue`) and the `return nil` at the tail of the Windows impl exist only to satisfy the compiler / handle the error path. Reviewers should not read the fallthrough as a normal-flow path.
- On Windows, `os.Exit(0)` in `reexecSelf` skips the `run()` deferred `m.info.Remove()`; the new detached monitor overwrites `server.json` on startup (existing `Start` behavior), leaving only a brief stale-PID window.
- No worker-side change is needed: the worker already signals upgrade via `ExitUpgrade` (4); this sub-project only changes how the monitor reacts. If a future sub-project adds an in-band "upgrade ready" health gate, it should slot before the `reexecFn` call without changing the seam.
