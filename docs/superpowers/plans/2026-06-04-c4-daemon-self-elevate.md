# C4 svc Self-Elevate (Detect + Guide Only) Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox syntax.

**Goal:** Make the PRIVILEGED OS-service verbs (`svc install` / `svc uninstall` / `svc start` / `svc stop` / `svc restart`) detect whether the current process holds Administrator/root and, when it does NOT, print platform-specific re-run guidance to stderr and abort with a new distinct exit code `ExitNeedsPrivilege=5`. This is DETECT-AND-GUIDE ONLY: never auto-elevate and never re-launch the binary elevated. `svc status` (unprivileged) and `svc run` (hidden, run by the OS service manager which is already elevated) are intentionally NOT gated.

**Architecture:** Everything lives inside `pkg/daemon` (package `daemon`) so the guard sits in the SAME package as the C5 `svc` verbs — there is NO unexported-symbol package-boundary problem (this is the fix for the previously blocked plan, which wrongly targeted `cmd/daemon` package `main` and the C2 `service` daemonize verbs). Two build-tag-split files (`elevate_windows.go` `//go:build windows`, `elevate_unix.go` `//go:build !windows`) each expose `isElevated() bool`, fronted by the package-var seam `isElevatedFn = isElevated` so orchestration is unit-testable while the raw syscalls are build-verified by cross-compile. The EXPORTED `RequirePrivilege(cmd *cobra.Command) error` checks the seam, writes platform guidance built from `cmd.CommandPath()` to `cmd.ErrOrStderr()`, and returns the EXPORTED sentinel `ErrNeedsPrivilege`. `ExitCodeFor(err error) int` (also in `pkg/daemon`) maps `ErrNeedsPrivilege -> 5`, else `ExitError -> 1` (etc.). The demo binary's `Execute()` (rewired in C5) already calls `os.Exit(daemon.ExitCodeFor(err))`, so wiring `ExitNeedsPrivilege` into `ExitCodeFor` is all C4 needs for exit-code propagation. `RequirePrivilege(cmd)` is wired as the FIRST statement of the `RunE` of `svc install/uninstall/start/stop/restart` (the C5 verbs).

**Tech Stack:** Go 1.25, `github.com/spf13/cobra` (already used in `pkg/daemon/cobra.go`), `golang.org/x/sys/windows` (ALREADY directly required at v0.34.0 — same import already used by `pkg/daemon/spawn_windows.go`), stdlib `os`/`errors`/`fmt`/`runtime`. No new third-party dependency and no module download.

**C5 DEPENDENCY (HARD PRECONDITION):** This plan gates verbs that C5 creates. The `svc` group and its constructors (`svcInstallCommand`/`svcUninstallCommand`/`svcStartCommand`/`svcStopCommand`/`svcRestartCommand`/`svcStatusCommand`/`svcRunCommand`, attached under a `svc` parent via `attachSvcCommands` inside `AttachCommands`) MUST already exist in `pkg/daemon` before Task 4 runs. If C5 used different constructor names, apply the identical guard to whatever the real `svc` verb `RunE`s are and update the `findSvcVerb` name list — the guard is verb-name-agnostic. Do NOT touch the C2 `service` group (`service` / `service start` / `service stop` / `service status` daemonize verbs); they are LOCKED-unchanged and are NOT privileged.

---

### Task 1: Add `ExitNeedsPrivilege=5` and `ExitCodeFor` to the exit-code protocol

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/exitstatus.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/exitstatus_test.go` (Create)

- [ ] **Step 1: Write the failing test asserting the new code, distinctness, and the `ExitCodeFor` mapping.**

Create `D:/weaver-sync/modules/daemon/pkg/daemon/exitstatus_test.go`:

```go
package daemon

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitNeedsPrivilegeValue(t *testing.T) {
	if ExitNeedsPrivilege != 5 {
		t.Fatalf("ExitNeedsPrivilege = %d, want 5", ExitNeedsPrivilege)
	}
	if ExitNeedsPrivilege.AsInt() != 5 {
		t.Fatalf("AsInt() = %d, want 5", ExitNeedsPrivilege.AsInt())
	}
}

func TestExitCodesAreDistinct(t *testing.T) {
	seen := map[int]ExitStatus{}
	for _, e := range []ExitStatus{ExitSuccess, ExitError, ExitRestart, ExitUpgrade, ExitNeedsPrivilege} {
		if prev, ok := seen[e.AsInt()]; ok {
			t.Fatalf("exit code %d reused by %d and %d", e.AsInt(), prev, e)
		}
		seen[e.AsInt()] = e
	}
}

func TestExitCodeForMapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, int(ExitSuccess)},
		{"privilege sentinel", ErrNeedsPrivilege, int(ExitNeedsPrivilege)},
		{"wrapped privilege sentinel", fmt.Errorf("svc install: %w", ErrNeedsPrivilege), int(ExitNeedsPrivilege)},
		{"generic error", errors.New("boom"), int(ExitError)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCodeFor(tc.err); got != tc.want {
				t.Fatalf("ExitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test, expect FAIL (undefined: `ExitNeedsPrivilege`, `ExitCodeFor`, `ErrNeedsPrivilege`).**

```powershell
go test ./pkg/daemon/ -run "TestExitNeedsPrivilege|TestExitCodesAreDistinct|TestExitCodeFor" -count=1
# expect: build fails — undefined: ExitNeedsPrivilege / ExitCodeFor / ErrNeedsPrivilege
```

Note: `ErrNeedsPrivilege` is defined in Task 3; this test will not compile until then. That is acceptable for the RED step — run it again at the end of Task 3. To keep Task 1 self-contained, the `TestExitCodeFor` sub-test may be temporarily commented out and re-enabled in Task 3 Step 4 (the implementer's choice; either way the GREEN gate is Task 3).

- [ ] **Step 3: Add the constant (ADD only — never reuse 0/1/3/4).**

In `D:/weaver-sync/modules/daemon/pkg/daemon/exitstatus.go`, replace the closing `)` of the existing `const (...)` block so the new member is appended INSIDE it. The existing members (`ExitSuccess=0`, `ExitError=1`, `ExitRestart=3`, `ExitUpgrade=4`) stay byte-for-byte. Change:

```go
	// ExitUpgrade signals the binary was replaced on disk; the monitor re-exec's itself
	// (syscall.Exec on Unix, spawn on Windows).
	ExitUpgrade ExitStatus = 4
)
```

to:

```go
	// ExitUpgrade signals the binary was replaced on disk; the monitor re-exec's itself
	// (syscall.Exec on Unix, spawn on Windows).
	ExitUpgrade ExitStatus = 4
	// ExitNeedsPrivilege is returned when a privileged svc verb (svc install/uninstall/
	// start/stop/restart) is invoked without admin/root. The process prints guidance and
	// aborts WITHOUT attempting the operation and WITHOUT re-launching elevated.
	ExitNeedsPrivilege ExitStatus = 5
)
```

- [ ] **Step 4: Add `ExitCodeFor` at the end of `exitstatus.go`.**

`ExitCodeFor` returns a plain `int` (per the locked design) so the demo `Execute()` can call `os.Exit(daemon.ExitCodeFor(err))` directly. It depends on `errors.Is` and the `ErrNeedsPrivilege` sentinel (Task 3); add the `"errors"` import. Append:

```go
import "errors"

// ExitCodeFor maps an error returned by the root command to the daemon exit-code protocol,
// returning a plain int for os.Exit. nil -> 0 (ExitSuccess); ErrNeedsPrivilege (even when
// wrapped) -> 5 (ExitNeedsPrivilege); any other non-nil error -> 1 (ExitError). New mappings
// may be ADDED here; existing codes are never reused.
func ExitCodeFor(err error) int {
	switch {
	case err == nil:
		return ExitSuccess.AsInt()
	case errors.Is(err, ErrNeedsPrivilege):
		return ExitNeedsPrivilege.AsInt()
	default:
		return ExitError.AsInt()
	}
}
```

Note: `exitstatus.go` currently has no imports. Add the `import "errors"` line directly under `package daemon` (gofmt/goimports will tidy it). `ErrNeedsPrivilege` is undefined until Task 3 — the package will not compile standalone until Task 3 lands its sentinel; that is expected and is resolved within this sequence. (If you prefer strictly-compiling intermediate commits, reorder so Task 3's `privilege.go` sentinel is committed alongside this file; the chosen ordering keeps the exit-code concerns in one commit and the guard in another — either is acceptable.)

- [ ] **Step 5: Defer GREEN to Task 3.** Because `ErrNeedsPrivilege` lands in Task 3, the GREEN run for this task's tests happens in Task 3 Step 4. Do NOT commit a non-compiling package in isolation — see Task 3 Step 5 for the combined commit covering `exitstatus.go` + `privilege.go` if you keep them together, OR commit `exitstatus.go` immediately after Task 3 defines the sentinel.

---

### Task 2: Platform elevation detection (`isElevated`) behind the `isElevatedFn` seam

**Files:**
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/elevate.go`
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/elevate_windows.go`
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/elevate_unix.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/elevate_test.go` (Create)

- [ ] **Step 1: Write the failing test for the seam default and toggle behavior.**

Create `D:/weaver-sync/modules/daemon/pkg/daemon/elevate_test.go`:

```go
package daemon

import "testing"

func TestIsElevatedFnDefaultsToIsElevated(t *testing.T) {
	// The seam must default to the platform implementation and be callable on every
	// supported OS without panicking. We assert only that it returns (no panic); the
	// concrete bool depends on how the test process was launched.
	_ = isElevatedFn()
}

func TestIsElevatedFnIsOverridable(t *testing.T) {
	orig := isElevatedFn
	t.Cleanup(func() { isElevatedFn = orig })

	isElevatedFn = func() bool { return true }
	if !isElevatedFn() {
		t.Fatal("override to true failed")
	}
	isElevatedFn = func() bool { return false }
	if isElevatedFn() {
		t.Fatal("override to false failed")
	}
}
```

- [ ] **Step 2: Run the test, expect FAIL (undefined: `isElevatedFn`).**

```powershell
go test ./pkg/daemon/ -run TestIsElevatedFn -count=1
# expect: build fails — undefined: isElevatedFn
```

- [ ] **Step 3: Create the shared seam file `elevate.go`.**

```go
package daemon

// isElevatedFn is the test seam fronting the platform isElevated implementation
// (elevate_windows.go / elevate_unix.go). Production points it at isElevated; tests
// override it to drive the privileged/unprivileged branches of RequirePrivilege.
var isElevatedFn = isElevated
```

- [ ] **Step 4: Create `elevate_windows.go` (build-verified by cross-compile).**

```go
//go:build windows

package daemon

import "golang.org/x/sys/windows"

// isElevated reports whether the current process token is elevated (Administrator).
// GetCurrentProcessToken returns a pseudo-token that needs no Close; IsElevated reads the
// token's elevation flag (TokenElevation). Verified against golang.org/x/sys v0.34.0, the
// same module/version already imported by spawn_windows.go.
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
```

- [ ] **Step 5: Create `elevate_unix.go`.**

```go
//go:build !windows

package daemon

import "os"

// isElevated reports whether the process runs with root privileges (effective uid 0).
func isElevated() bool {
	return os.Geteuid() == 0
}
```

- [ ] **Step 6: Run the test, expect PASS.**

```powershell
go test ./pkg/daemon/ -run TestIsElevatedFn -count=1
# expect: ok  github.com/inovacc/daemon/pkg/daemon
```

- [ ] **Step 7: Commit.**

```powershell
git add pkg/daemon/elevate.go pkg/daemon/elevate_windows.go pkg/daemon/elevate_unix.go pkg/daemon/elevate_test.go
git commit -m "feat(daemon): detect admin/root via build-tag-split isElevated seam"
```

---

### Task 3: Exported `RequirePrivilege` guard + `ErrNeedsPrivilege` sentinel

**Files:**
- Create: `D:/weaver-sync/modules/daemon/pkg/daemon/privilege.go`
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/privilege_test.go` (Create)

- [ ] **Step 1: Write the failing test for both branches (guidance built from `CommandPath()` + sentinel when not elevated; nil + silence when elevated).**

Create `D:/weaver-sync/modules/daemon/pkg/daemon/privilege_test.go`:

```go
package daemon

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// buildPath constructs a realistic command tree so cmd.CommandPath() returns
// "daemon svc install" — the exact string RequirePrivilege embeds in its guidance.
func buildPath(t *testing.T, verb string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "daemon"}
	svc := &cobra.Command{Use: "svc"}
	leaf := &cobra.Command{Use: verb}
	svc.AddCommand(leaf)
	root.AddCommand(svc)
	return leaf
}

func TestRequirePrivilegeBlocksWhenNotElevated(t *testing.T) {
	orig := isElevatedFn
	t.Cleanup(func() { isElevatedFn = orig })
	isElevatedFn = func() bool { return false }

	leaf := buildPath(t, "install")
	var stderr bytes.Buffer
	leaf.SetErr(&stderr)

	err := RequirePrivilege(leaf)
	if !errors.Is(err, ErrNeedsPrivilege) {
		t.Fatalf("err = %v, want ErrNeedsPrivilege", err)
	}
	out := stderr.String()
	// Guidance must reference the full command path so the user can copy-paste the re-run.
	if !strings.Contains(out, "daemon svc install") {
		t.Fatalf("guidance missing command path %q: %q", "daemon svc install", out)
	}
	low := strings.ToLower(out)
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(low, "runas") && !strings.Contains(low, "administrator") {
			t.Fatalf("windows guidance missing RunAs/administrator hint: %q", out)
		}
	default:
		if !strings.Contains(low, "sudo") {
			t.Fatalf("unix guidance missing sudo hint: %q", out)
		}
	}
}

func TestRequirePrivilegeProceedsWhenElevated(t *testing.T) {
	orig := isElevatedFn
	t.Cleanup(func() { isElevatedFn = orig })
	isElevatedFn = func() bool { return true }

	leaf := buildPath(t, "install")
	var stderr bytes.Buffer
	leaf.SetErr(&stderr)

	if err := RequirePrivilege(leaf); err != nil {
		t.Fatalf("RequirePrivilege returned %v, want nil when elevated", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("no guidance expected when elevated, got %q", stderr.String())
	}
}
```

- [ ] **Step 2: Run the test, expect FAIL (undefined: `RequirePrivilege`, `ErrNeedsPrivilege`).**

```powershell
go test ./pkg/daemon/ -run TestRequirePrivilege -count=1
# expect: build fails — undefined: RequirePrivilege / ErrNeedsPrivilege
```

- [ ] **Step 3: Create `privilege.go` with the sentinel, guidance, and guard.**

Both platform messages are always compiled; `runtime.GOOS` selects which to print (no build tag needed for the guidance — only the detection in Task 2 is build-split). The re-run command is built from `cmd.CommandPath()` so it includes the binary and full verb path (`daemon svc install`).

```go
package daemon

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// ErrNeedsPrivilege is returned by RequirePrivilege when a privileged svc verb is invoked
// without admin/root. ExitCodeFor maps it (even when wrapped) to ExitNeedsPrivilege (5).
var ErrNeedsPrivilege = errors.New("daemon: operation requires administrator/root privileges")

// RequirePrivilege is the FIRST call in every privileged svc verb's RunE (svc install/
// uninstall/start/stop/restart). When the process is NOT elevated it writes platform-specific
// re-run guidance — built from cmd.CommandPath() — to cmd.ErrOrStderr() and returns
// ErrNeedsPrivilege WITHOUT performing the operation and WITHOUT re-launching elevated
// (detect-and-guide only). When elevated it returns nil and the verb proceeds.
func RequirePrivilege(cmd *cobra.Command) error {
	if isElevatedFn() {
		return nil
	}
	w := cmd.ErrOrStderr()
	path := cmd.CommandPath() // e.g. "daemon svc install"
	switch runtime.GOOS {
	case "windows":
		fmt.Fprintf(w, "%q requires administrator privileges.\n", path)
		fmt.Fprintln(w, "Re-run from an elevated PowerShell, for example:")
		fmt.Fprintf(w, "  Start-Process powershell -Verb RunAs -ArgumentList '%s'\n", path)
	default:
		fmt.Fprintf(w, "%q requires root privileges.\n", path)
		fmt.Fprintln(w, "Re-run with sudo, for example:")
		fmt.Fprintf(w, "  sudo %s\n", path)
	}
	return ErrNeedsPrivilege
}
```

- [ ] **Step 4: Run the privilege AND exit-status tests, expect PASS.**

Now that `ErrNeedsPrivilege` exists, the Task 1 tests compile too. Run both suites:

```powershell
go test ./pkg/daemon/ -run "TestRequirePrivilege|TestExitNeedsPrivilege|TestExitCodesAreDistinct|TestExitCodeFor" -count=1
# expect: ok  github.com/inovacc/daemon/pkg/daemon
```

- [ ] **Step 5: Commit Task 1 + Task 3 together (so every commit compiles).**

```powershell
git add pkg/daemon/exitstatus.go pkg/daemon/exitstatus_test.go pkg/daemon/privilege.go pkg/daemon/privilege_test.go
git commit -m "feat(daemon): add ExitNeedsPrivilege=5, ExitCodeFor, and RequirePrivilege guard"
```

---

### Task 4: Wire `RequirePrivilege` into the C5 `svc` privileged verbs

**PRECONDITION (C5):** The `svc` group exists in `pkg/daemon` (constructors `svcInstallCommand`/`svcUninstallCommand`/`svcStartCommand`/`svcStopCommand`/`svcRestartCommand`/`svcStatusCommand`/`svcRunCommand`, attached under a `svc` parent by C5's `attachSvcCommands`, called from `AttachCommands`). This task edits only the five privileged verb `RunE`s. It does NOT touch the C2 `service`/`__monitor`/`__worker` wiring, and it does NOT gate `svc status` or `svc run`.

**Files:**
- Modify: `D:/weaver-sync/modules/daemon/pkg/daemon/svc.go` (the C5 svc verb constructors — adjust the filename if C5 placed them elsewhere)
- Test: `D:/weaver-sync/modules/daemon/pkg/daemon/svc_privilege_test.go` (Create)

- [ ] **Step 1: Write the failing test asserting each privileged svc verb is gated, and that `status`/`run` are NOT.**

This builds the tree via `AttachCommands`, locates each verb under the `svc` parent, toggles the seam, executes `RunE`, and asserts the not-elevated path returns `ErrNeedsPrivilege` + prints guidance while `status`/`run` are never gated. The elevated path uses the C5 `osService` seam (`newOSService`) so the verb body can run inertly against a fake; if your C5 seam differs, swap the fake wiring accordingly.

Create `D:/weaver-sync/modules/daemon/pkg/daemon/svc_privilege_test.go`:

```go
package daemon

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// findSvcVerb locates a leaf verb under the `svc` parent built by AttachCommands.
func findSvcVerb(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, svc := range root.Commands() {
		if svc.Name() != "svc" {
			continue
		}
		for _, v := range svc.Commands() {
			if v.Name() == name {
				return v
			}
		}
	}
	t.Fatalf("verb %q not found under svc", name)
	return nil
}

func newTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "daemon"}
	if err := AttachCommands(root, Options{
		BinaryName: "daemon",
		DataDir:    t.TempDir(),
		Serve:      func(context.Context, Ports) error { return nil },
	}); err != nil {
		t.Fatalf("AttachCommands: %v", err)
	}
	return root
}

var privilegedSvcVerbs = []string{"install", "uninstall", "start", "stop", "restart"}

func TestSvcPrivilegedVerbsBlockedWhenNotElevated(t *testing.T) {
	orig := isElevatedFn
	t.Cleanup(func() { isElevatedFn = orig })
	isElevatedFn = func() bool { return false }

	root := newTestRoot(t)
	for _, name := range privilegedSvcVerbs {
		t.Run(name, func(t *testing.T) {
			v := findSvcVerb(t, root, name)
			var stderr bytes.Buffer
			v.SetErr(&stderr)
			err := v.RunE(v, nil)
			if !errors.Is(err, ErrNeedsPrivilege) {
				t.Fatalf("svc %s RunE = %v, want ErrNeedsPrivilege", name, err)
			}
			if stderr.Len() == 0 {
				t.Fatalf("svc %s printed no guidance", name)
			}
		})
	}
}

// fakeOSService records which verb methods were called so we can assert the elevated path
// reaches the C5 osService seam rather than short-circuiting on privilege.
type fakeOSService struct{ calls map[string]int }

func (f *fakeOSService) note(s string)        { f.calls[s]++ }
func (f *fakeOSService) Install() error        { f.note("Install"); return nil }
func (f *fakeOSService) Uninstall() error      { f.note("Uninstall"); return nil }
func (f *fakeOSService) Start() error          { f.note("Start"); return nil }
func (f *fakeOSService) Stop() error           { f.note("Stop"); return nil }
func (f *fakeOSService) Restart() error        { f.note("Restart"); return nil }
func (f *fakeOSService) Status() (string, error) { f.note("Status"); return "running", nil }
func (f *fakeOSService) Run() error            { f.note("Run"); return nil }

func TestSvcPrivilegedVerbsProceedWhenElevated(t *testing.T) {
	origElev := isElevatedFn
	origNew := newOSService
	t.Cleanup(func() {
		isElevatedFn = origElev
		newOSService = origNew
	})
	isElevatedFn = func() bool { return true }

	fake := &fakeOSService{calls: map[string]int{}}
	newOSService = func(Options) (osService, error) { return fake, nil }

	root := newTestRoot(t)
	want := map[string]string{
		"install":   "Install",
		"uninstall": "Uninstall",
		"start":     "Start",
		"stop":      "Stop",
		"restart":   "Restart",
	}
	for _, name := range privilegedSvcVerbs {
		t.Run(name, func(t *testing.T) {
			v := findSvcVerb(t, root, name)
			var out bytes.Buffer
			v.SetOut(&out)
			v.SetErr(&out)
			if err := v.RunE(v, nil); errors.Is(err, ErrNeedsPrivilege) {
				t.Fatalf("svc %s returned ErrNeedsPrivilege while elevated", name)
			}
			if fake.calls[want[name]] == 0 {
				t.Fatalf("svc %s did not reach osService.%s when elevated", name, want[name])
			}
		})
	}
}

func TestSvcStatusAndRunNotGated(t *testing.T) {
	origElev := isElevatedFn
	origNew := newOSService
	t.Cleanup(func() {
		isElevatedFn = origElev
		newOSService = origNew
	})
	// Not elevated — status/run must STILL work (they are intentionally ungated).
	isElevatedFn = func() bool { return false }
	fake := &fakeOSService{calls: map[string]int{}}
	newOSService = func(Options) (osService, error) { return fake, nil }

	root := newTestRoot(t)
	for _, name := range []string{"status", "run"} {
		t.Run(name, func(t *testing.T) {
			v := findSvcVerb(t, root, name)
			var out bytes.Buffer
			v.SetOut(&out)
			v.SetErr(&out)
			if err := v.RunE(v, nil); errors.Is(err, ErrNeedsPrivilege) {
				t.Fatalf("svc %s was gated but must be unprivileged", name)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test, expect FAIL (verbs not yet gated; not-elevated path returns nil or the op result, not `ErrNeedsPrivilege`).**

```powershell
go test ./pkg/daemon/ -run "TestSvcPrivilegedVerbs|TestSvcStatusAndRunNotGated" -count=1
# expect: FAIL — svc install/uninstall/start/stop/restart RunE returns nil, not ErrNeedsPrivilege
```

- [ ] **Step 3: Gate each privileged svc verb's `RunE` with `RequirePrivilege` as the FIRST statement.**

In the C5 svc constructors (`svc.go`), prepend the guard to the `RunE` of `svcInstallCommand`, `svcUninstallCommand`, `svcStartCommand`, `svcStopCommand`, `svcRestartCommand`. Do NOT add it to `svcStatusCommand` or `svcRunCommand`. The exact snippet to paste as the first statement of each gated `RunE` (before any `newOSService`/service-manager call) is:

```go
			if err := RequirePrivilege(cmd); err != nil {
				return err
			}
```

For concreteness, each gated constructor reads (the `// ... C5 <verb> body ...` marks the existing C5 logic that stays unchanged BELOW the guard — do not alter it):

```go
func svcInstallCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register the OS service with the init system (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := RequirePrivilege(cmd); err != nil {
				return err
			}
			// ... C5 install body (newOSService(o) -> svc.Install()) ...
			return nil
		},
	}
}

func svcUninstallCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the OS service from the init system (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := RequirePrivilege(cmd); err != nil {
				return err
			}
			// ... C5 uninstall body (newOSService(o) -> svc.Uninstall()) ...
			return nil
		},
	}
}

func svcStartCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Ask the init system to start the service (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := RequirePrivilege(cmd); err != nil {
				return err
			}
			// ... C5 start body (newOSService(o) -> svc.Start()) ...
			return nil
		},
	}
}

func svcStopCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Ask the init system to stop the service (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := RequirePrivilege(cmd); err != nil {
				return err
			}
			// ... C5 stop body (newOSService(o) -> svc.Stop()) ...
			return nil
		},
	}
}

func svcRestartCommand(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Ask the init system to restart the service (privileged)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := RequirePrivilege(cmd); err != nil {
				return err
			}
			// ... C5 restart body (newOSService(o) -> svc.Restart()) ...
			return nil
		},
	}
}
```

`svcStatusCommand` (calls `Status()`, read-only) and `svcRunCommand` (`Hidden:true`, invoked by an already-elevated service manager to run the supervisor) keep their C5 `RunE` UNCHANGED — no guard.

- [ ] **Step 4: Run the test, expect PASS.**

```powershell
go test ./pkg/daemon/ -run "TestSvcPrivilegedVerbs|TestSvcStatusAndRunNotGated" -count=1
# expect: ok  github.com/inovacc/daemon/pkg/daemon
```

- [ ] **Step 5: Commit.**

```powershell
git add pkg/daemon/svc.go pkg/daemon/svc_privilege_test.go
git commit -m "feat(daemon): gate privileged svc verbs behind RequirePrivilege"
```

---

### Task 5: Full verification, cross-compile, dependency pin, and logger-module purity

**Files:**
- (No source changes — verification only.)

- [ ] **Step 1: Build and vet the daemon module (native).**

```powershell
go build ./...      # expect exit 0
go vet ./...        # expect exit 0
```

- [ ] **Step 2: Cross-compile for each GOOS with GOARCH=amd64, and `go vet` under each (every command exits 0).**

Run from the daemon module root (`D:/weaver-sync/modules/daemon`). `elevate_windows.go` compiles only under `GOOS=windows`; `elevate_unix.go` under linux/darwin.

```powershell
$env:GOARCH = "amd64"
foreach ($os in @("windows","linux","darwin")) {
  $env:GOOS = $os
  Write-Host "== $os/amd64 build =="; go build ./...; if ($LASTEXITCODE -ne 0) { throw "build failed: $os" }
  Write-Host "== $os/amd64 vet ==";   go vet ./...;   if ($LASTEXITCODE -ne 0) { throw "vet failed: $os" }
}
Remove-Item Env:GOOS
Remove-Item Env:GOARCH
# expect: all six commands exit 0
```

- [ ] **Step 3: Run the full test suite with the race detector.**

```powershell
go test ./... -race -count=1
# expect: ok for all packages (pkg/daemon, pkg/serverinfo, ...)
```

- [ ] **Step 4: Dependency hygiene — prove no drift; pins unchanged.**

No new third-party dependency is introduced: `golang.org/x/sys` is already directly required at v0.34.0 (used by `spawn_windows.go` and now `elevate_windows.go`); `github.com/kardianos/service` stays pinned at v1.2.2 (added by C5, not bumped here). NEVER use `@latest`.

```powershell
go mod tidy
go list -m golang.org/x/sys              # expect: golang.org/x/sys v0.34.0
go list -m github.com/kardianos/service  # expect: github.com/kardianos/service v1.2.2
git diff --exit-code go.mod go.sum       # expect: no changes (exit 0)
```

If `go mod tidy` changes anything, revert and investigate — C4 must not bump `golang.org/x/sys` (v0.34.0) or `github.com/kardianos/service` (v1.2.2). (Spike note: a clean throwaway module resolved `x/sys` to a newer tag; inside the daemon module the existing v0.34.0 pin governs and already exports `GetCurrentProcessToken().IsElevated()`.)

- [ ] **Step 5: Logger-module purity check (no `**` glob — use `Get-ChildItem -Recurse -Filter`).**

The privileged-verb feature and its only third-party import (`golang.org/x/sys/windows`, plus the kardianos import C5 added) must live ONLY in the daemon module and never leak into the logger module's `pkg/logger` or `pkg/obsv`. Both must import ZERO kardianos (and zero of this feature's symbols):

```powershell
$hits = Get-ChildItem -Path "D:/weaver-sync/modules/logger/pkg/logger","D:/weaver-sync/modules/logger/pkg/obsv" -Recurse -Filter *.go |
  Select-String -SimpleMatch -Pattern "kardianos","golang.org/x/sys","isElevated","RequirePrivilege","ErrNeedsPrivilege","ExitNeedsPrivilege"
if ($hits) { $hits; throw "purity violation: logger module references daemon/kardianos internals" }
"purity OK: zero kardianos / x/sys / elevation references in pkg/logger and pkg/obsv"
# expect: "purity OK: ..." and exit 0 (no hits)
```

- [ ] **Step 6: Final commit (only if `go mod tidy` produced legitimate, intended changes; otherwise skip — there should be none).**

```powershell
git add -A
git commit -m "chore(daemon): verify C4 svc self-elevate build, cross-compile, and pins"
```

---

## Self-Review

Decision-to-task mapping (locked design item 6):

- **New distinct exit code `ExitNeedsPrivilege=5`, ADDED never reused, plus `ExitCodeFor(err) int`** → **Task 1**. `TestExitCodesAreDistinct` proves 0/1/3/4/5 are unique; `TestExitCodeFor` proves `ErrNeedsPrivilege` (even wrapped) → 5, generic error → 1, nil → 0. `ExitCodeFor` returns a plain `int` so the C5-rewired demo `Execute()` calls `os.Exit(daemon.ExitCodeFor(err))` directly — C4 adds NO new `Execute`/consumer plumbing (that was the blocked plan's overreach).
- **Per-OS detection split by build tag** (`elevate_windows.go` `//go:build windows`, `elevate_unix.go` `//go:build !windows`), each exposing `isElevated() bool`, fronted by the package-var seam `isElevatedFn = isElevated` → **Task 2**. Verified API: `windows.GetCurrentProcessToken().IsElevated()` (same `golang.org/x/sys/windows` v0.34.0 import already in `spawn_windows.go`) and `os.Geteuid() == 0` (Unix). kardianos itself is only build-verified; this detection has no kardianos dependency.
- **EXPORTED `RequirePrivilege(cmd *cobra.Command) error`** writes guidance built from `cmd.CommandPath()` to `cmd.ErrOrStderr()` (Windows: `Start-Process powershell -Verb RunAs -ArgumentList '<path>'`; Unix: `sudo <path>`) and returns the EXPORTED sentinel `ErrNeedsPrivilege` when `!isElevatedFn()`, WITHOUT attempting the op and WITHOUT re-launching elevated → **Task 3**.
- **`RequirePrivilege` wired as the FIRST statement of the RunE of the C5 `svc install/uninstall/start/stop/restart`** (NOT `svc status`, NOT `svc run`) → **Task 4**. Tests toggle the seam false (assert guidance + `ErrNeedsPrivilege` for all five) and true (assert each verb proceeds to the C5 `osService` seam via `newOSService`), plus a test proving `status`/`run` stay ungated even when not elevated.
- **Verification**: native build+vet, cross-compile windows/linux/darwin at amd64 WITH `go vet` under each, `-race` tests, `go mod tidy` + `git diff --exit-code` proving x/sys stays v0.34.0 and kardianos v1.2.2 (never `@latest`), and a logger-module purity check using `Get-ChildItem -Recurse -Filter *.go | Select-String -SimpleMatch` (NOT a `**` glob) → **Task 5**.

Invariants honored:
- Everything is in `pkg/daemon` (package `daemon`). Because the C5 `svc` verbs are in the SAME package, `RequirePrivilege` is called directly — there is NO unexported-symbol package-boundary problem. This is the precise fix for the previously blocked plan, which targeted `cmd/daemon` package `main` and (worse) the C2 `service` daemonize verbs.
- The C2 `service` group (`service` / `service start` / `service stop` / `service status` lightweight background daemonize, plus hidden `__monitor`/`__worker`) is UNCHANGED and UNGATED. Those verbs are not privileged and the locked design forbids touching them.
- Exit-code protocol extended additively (5 added; 0/1/3/4 untouched). Detect-and-guide only: no auto-elevation, no elevated re-launch.
- The feature never leaks into the logger module's `pkg/logger`/`pkg/obsv`; kardianos and x/sys stay confined to the daemon module.
- Conventional commits, ZERO AI attribution and ZERO `Co-Authored-By` lines.

C5 DEPENDENCY (stated explicitly): Task 4 gates verbs C5 creates. The `svc` group, its constructors (`svcInstallCommand`/`svcUninstallCommand`/`svcStartCommand`/`svcStopCommand`/`svcRestartCommand`/`svcStatusCommand`/`svcRunCommand`), the `attachSvcCommands` wiring inside `AttachCommands`, the `osService` interface, and the `newOSService` seam MUST exist before Task 4 runs. If C5 named a verb or constructor differently, apply the identical first-statement `RequirePrivilege(cmd)` guard to whatever the real privileged `svc` verb `RunE`s are and update `findSvcVerb`'s name list and the `newOSService`/`osService` fake wiring in `svc_privilege_test.go` accordingly — the guard, sentinel, and exit mapping are verb-name-agnostic. Tasks 1–3 (exit code, detection, guard) have NO C5 dependency and can land first; only Task 4 (and the elevated-path assertions in its test) require C5.
