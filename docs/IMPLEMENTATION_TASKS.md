# Mantle — Implementation Tasks

> Brand: **Mantle** (github.com/inovacc/logger — module-path rename to `.../mantle` pending).
> Go 1.25 | License BSD-3-Clause | Maintainer: Dyam Marcano <dyam.marcano@gmail.com>.
> Part of the inovacc fleet (config, daemon, logger=Mantle).

This document breaks the **PLANNED** work — milestone **v0.2.0 "Daemon"** (Phase 4) plus
Phase 5 hardening — into granular, dependency-aware tasks grouped by domain.

Foundation (v0.1.0) is **DONE**: `pkg/logger` (81.5%), `pkg/obsv` (86.6%),
`pkg/bootstrap` (91.3%), `cmd/logger` demo (0% — no tests). End-to-end
cobra → wrapper → core already works. Everything below is net-new.

**Effort key:** S = ≤0.5 day · M = 1–2 days · L = 3–5 days.
Platform-split items use the fleet convention `*_windows.go` / `*_unix.go`.

---

## Domain 1 — Daemon module (`inovacc/daemon`)

Separate repository at `D:\weaver-sync\modules\daemon`. This is sub-project **C**:
build out the sibling module's gaps so `pkg/bootstrap` can wire daemon mode in
Domain 2. Lifecycle flow mirrors Mantle's runtime: a self-spawned child runs the
core handler as a long-lived `Serve` loop, with self-update, self-elevate, and
persistent service install around it.

### Task 1.1 — Self-spawn `<APP>_DAEMON_CHILD` guard
- **What:** Re-exec the binary as a detached child so the parent returns control
  to the shell. Guard against infinite self-spawn by setting and checking an env
  marker `<APP>_DAEMON_CHILD=1` (APP derived from the binary/service name); when
  the marker is present the process runs the actual `Serve` loop instead of
  re-spawning. Parent waits for the child to report readiness, then exits 0.
- **Files:** `daemon/spawn.go` (marker name builder, env detection, generic
  parent/child branch), `daemon/spawn_windows.go` (spawn detached via
  `os/exec` + `CREATE_NEW_PROCESS_GROUP`), `daemon/spawn_unix.go` (fork-style
  re-exec, `setsid`).
- **Environment:** `<APP>_DAEMON_CHILD` (set by parent, read by child).
- **Dependencies:** none (root of the daemon graph).
- **Effort:** M

### Task 1.2 — Self-update via `ExitUpgrade(4)` re-exec
- **What:** On an upgrade signal the running process exits with the sentinel code
  `ExitUpgrade = 4`; the supervising parent (or service manager) detects code 4,
  swaps the binary, and re-execs. Re-exec uses `syscall.Exec` on Unix (in-place
  image replacement) and spawn-then-exit on Windows (no `execve` equivalent).
  Include binary-replacement detection (compare on-disk hash/mtime/inode of the
  running executable vs. the path resolved at start) so the parent knows a new
  binary is present before re-exec.
- **Files:** `daemon/upgrade.go` (`ExitUpgrade` const, replacement-detection
  helper, re-exec dispatcher), `daemon/upgrade_unix.go` (`syscall.Exec`),
  `daemon/upgrade_windows.go` (spawn replacement + exit current).
- **Environment:** reuses `<APP>_DAEMON_CHILD`; relies on the supervisor loop
  established in 1.1.
- **Dependencies:** **1.1** (needs the parent/child supervisor to observe exit 4).
- **Effort:** L

### Task 1.3 — Self-elevate (UAC / sudo|polkit)
- **What:** When an operation requires privileges (e.g. service install), detect
  the current privilege level and re-launch elevated. Windows: trigger UAC via
  `ShellExecute`/`runas` verb. Unix: re-exec under `sudo`, falling back to
  `pkexec` (polkit) when `sudo` is unavailable. Preserve the original argv and
  the `<APP>_DAEMON_CHILD` marker across elevation.
- **Files:** `daemon/elevate.go` (privilege check abstraction, elevation
  dispatcher), `daemon/elevate_windows.go` (UAC/`runas`), `daemon/elevate_unix.go`
  (`sudo` → `pkexec` fallback).
- **Environment:** inherits `<APP>_DAEMON_CHILD`; may set an
  `<APP>_DAEMON_ELEVATED` guard to avoid elevation loops.
- **Dependencies:** **1.1** (argv/marker preservation).
- **Effort:** M

### Task 1.4 — Persistent `kardianos/service` install lifecycle
- **What:** Wrap `github.com/kardianos/service` to expose the full lifecycle:
  `install`, `uninstall`, `start`, `stop`, `status`, `run`. `run` is the in-service
  entrypoint that drives the `Serve` loop; the others manage the platform service
  unit (systemd / Windows SCM / launchd). Surface these as subcommands the
  bootstrap layer can attach. Use 1.3 to self-elevate `install`/`uninstall`.
- **Files:** `daemon/service.go` (`service.Service` wiring, lifecycle command
  set, `Serve`/`Stop` interface impl), `daemon/service_config.go` (service name,
  display name, description, dependencies).
- **Environment:** none new (service manager owns the runtime env).
- **Dependencies:** **1.1** (run loop), **1.3** (elevated install/uninstall).
- **Effort:** L

### Task 1.5 — Structured slog lifecycle hooks
- **What:** Emit structured `log/slog` records at every lifecycle transition:
  **startup, restart (+backoff), loop-abort, worker-start, signal, shutdown,
  exit, upgrade**. Hooks accept the Mantle logger (a `*slog.Logger`) so records
  flow through the redaction → trace → fanout chain. Backoff is logged with the
  computed delay and attempt count; signal hook logs the received signal name.
- **Files:** `daemon/hooks.go` (hook-point definitions, default slog
  implementations), `daemon/backoff.go` (restart backoff policy + logged delays).
- **Environment:** none.
- **Dependencies:** **1.1**, **1.2**, **1.4** (instruments their transitions).
- **Effort:** M

---

## Domain 2 — Bootstrap wiring (`pkg/bootstrap`)

Integrate the daemon module into Mantle's runtime so `--daemon` (or
`Features.Daemon`) turns any Configurable binary into a daemon-capable one without
the core app changing. Preserves the architecture invariant that disabled
subsystems are no-op (never nil) so core code stays branch-free.

### Task 2.1 — Add `Features.Daemon` + `--daemon` flag
- **What:** Extend `Features{Logging,Observability}` with a `Daemon` bool and add
  `--daemon` to the always-present persistent flag set (currently 11 flags). Wire
  it through the same CHANGED-flag overlay pass that gives flags highest
  precedence over file+env, and evaluate it in `Features` during
  `PersistentPreRunE`. Update `DefaultBase()` to seed the programmatic default
  (daemon off).
- **Files:** `pkg/bootstrap/base.go` (Features struct, DefaultBase),
  `pkg/bootstrap/flags.go` (register `--daemon`, overlay handling),
  `pkg/bootstrap/configure.go` (feature evaluation).
- **Environment:** none (flag + config-key only).
- **Dependencies:** Domain 1 complete enough to import (at least **1.1**, **1.4**).
- **Effort:** S

### Task 2.2 — Delegate to `daemon.AttachCommands`/Options, passing core handler as `Serve`
- **What:** When `Features.Daemon` is enabled, call into the daemon module to
  attach its lifecycle subcommands (install/uninstall/start/stop/status/run from
  1.4) to the root cobra command, passing the core app handler — the same handler
  `bootstrap.Run` invokes — as the daemon's `Serve` function. The self-spawn
  guard (1.1) and lifecycle hooks (1.5) receive the Mantle `Runtime.Logger` so
  daemon logs share the redaction/trace/fanout chain.
- **Files:** `pkg/bootstrap/daemon.go` (new — adapter from `Runtime` +core handler
  to `daemon.Options`/`AttachCommands`), `pkg/bootstrap/configure.go` (invoke the
  adapter when the feature is on).
- **Environment:** propagates `<APP>_DAEMON_CHILD` (set/consumed inside the daemon
  module; bootstrap only forwards APP name).
- **Dependencies:** **2.1**, daemon **1.1 / 1.4 / 1.5**.
- **Effort:** M

### Task 2.3 — Chain daemon shutdown into `Runtime.Shutdown`
- **What:** Compose the daemon's stop/cleanup into the existing
  `Runtime.Shutdown` so a single shutdown call tears down obsv, logger sinks, and
  the daemon loop in the correct order (daemon stop → flush logs → obsv shutdown).
  Keep the no-op contract: when daemon is disabled, the chained shutdown step is a
  no-op, never nil.
- **Files:** `pkg/bootstrap/runtime.go` (extend `Shutdown` composition),
  `pkg/bootstrap/daemon.go` (expose the daemon stop func to chain).
- **Environment:** none.
- **Dependencies:** **2.2**.
- **Effort:** S

---

## Domain 3 — Hardening (Phase 5)

Close the quality gaps that block a tagged v0.2.0 release.

### Task 3.1 — `cmd/logger` tests
- **What:** Raise the reference binary `logger-demo` from 0% toward the 80% fleet
  target. Cover: `App` squash-embedding `bootstrap.Base`, `RunE` calling
  `bootstrap.Run` with the core handler, the always-present flags overriding
  file+env, and the auto-created `config.yaml` behavior when run without
  `--config`. Use the injectable fake `ConfigSource` to avoid the global config
  singleton.
- **Files:** `cmd/logger/main_test.go` (and a small testable seam in
  `cmd/logger/main.go` if `RunE` is not currently exported/injectable).
- **Environment:** test-only (`-race`); temp dir for the auto-created config.
- **Dependencies:** none (can start immediately, in parallel with Domain 1).
- **Effort:** M

### Task 3.2 — Module rename to `mantle`
- **What:** Rename the module path `github.com/inovacc/logger` →
  `github.com/inovacc/mantle` and update all internal import paths. This is the
  pending invasive change flagged in the brief; per the fleet's breaking-change
  policy, land it as a dedicated commit (not mixed with features) and update every
  consumer reference. Keep package directory names (`pkg/logger`, `pkg/obsv`,
  `pkg/bootstrap`, `cmd/logger`) unless a separate decision says otherwise.
- **Files:** `go.mod` (module path), every `*.go` import of `inovacc/logger/...`,
  `README.md` / `docs/*` references.
- **Environment:** none.
- **Dependencies:** done **after** features land to avoid rebasing churn — i.e.
  after **2.x** and **3.1** (gate before the tagged release).
- **Effort:** M

### Task 3.3 — CI + Taskfile + golangci-lint
- **What:** Add the missing toolchain scaffolding: a `Taskfile.yml` exposing
  `build` (`go build ./...`), `vet` (`go vet ./...`), `test`
  (`go test ./... -race`), `cover` (coverprofile), and `lint`
  (`golangci-lint run --fix ./... --timeout=5m`); a CI workflow running the same on
  push/PR; and a `.golangci.yml` per fleet convention. CI must also enforce the
  import-graph purity invariant via `go list -deps` checks (logger graph =
  stdlib + otel/trace only; SDK only in obsv; cobra + inovacc/config only in
  bootstrap).
- **Files:** `Taskfile.yml`, `.github/workflows/ci.yml` (or fleet-standard CI
  path), `.golangci.yml`, optional `scripts/check-import-graph.sh`.
- **Environment:** CI runner env only.
- **Dependencies:** none functionally, but most valuable once **2.x** code exists
  to lint; run before **3.4**.
- **Effort:** M

### Task 3.4 — Tagged release
- **What:** Cut the **v0.2.0 "Daemon"** tag once all features, tests, lint, and the
  module rename are in. Verify coverage holds at ≥80% across tested packages,
  generate release notes from conventional commits (feat/fix/test/docs/build/
  chore), and tag on master. No git remote exists yet, so this includes adding the
  remote as a prerequisite step.
- **Files:** `docs/ROADMAP.md` / `docs/BACKLOG.md` (mark milestone done, move the
  pending rename item to DONE), `CHANGELOG.md` if adopted.
- **Environment:** release/CI env.
- **Dependencies:** **everything** — **2.1–2.3**, **3.1**, **3.2**, **3.3**.
- **Effort:** S

---

## Dependency-ordered implementation sequence

Two tracks run in parallel up front: the **daemon module** (Domain 1) and the
independent **cmd/logger tests** (3.1). They converge at the bootstrap wiring.

1. **1.1** Self-spawn `<APP>_DAEMON_CHILD` guard — *(root of daemon graph)*
   — in parallel with **3.1** `cmd/logger` tests.
2. **1.2** Self-update `ExitUpgrade(4)` re-exec + binary-replacement detection
   — needs 1.1's supervisor.
3. **1.3** Self-elevate (UAC / sudo|polkit) — needs 1.1's argv/marker preservation.
4. **1.4** Persistent `kardianos/service` install lifecycle — needs 1.1 (run loop)
   + 1.3 (elevated install/uninstall).
5. **1.5** Structured slog lifecycle hooks — instruments 1.1 / 1.2 / 1.4.
   *(Daemon module now importable.)*
6. **2.1** `Features.Daemon` + `--daemon` flag — first bootstrap task once Domain 1 lands.
7. **2.2** Delegate to `daemon.AttachCommands`/Options, core handler as `Serve` — needs 2.1 + daemon.
8. **2.3** Chain daemon shutdown into `Runtime.Shutdown` — needs 2.2.
9. **3.3** CI + Taskfile + golangci-lint — light up after 2.x so it lints real code.
10. **3.2** Module rename to `mantle` — dedicated commit after features + tests, before release.
11. **3.4** Tagged release v0.2.0 — gate on all of the above.

**Critical path:** 1.1 → 1.2/1.4 → 1.5 → 2.1 → 2.2 → 2.3 → 3.3 → 3.2 → 3.4.
**Parallelizable:** 3.1 (anytime), 1.3 (after 1.1), and most of 3.3's scaffolding.
