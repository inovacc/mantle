# Mantle — Backlog

Future work and technical debt for **Mantle** (`github.com/inovacc/logger`), the batteries-included Go application runtime that wraps any binary with PII-redacting structured logging, full OpenTelemetry observability, and feature-flagged unified config.

Flow: cobra (entry) → bootstrap (wrapper/runtime) → core app.

Items are prioritized **P1** (next), **P2** (soon), **P3** (later), each with a rough effort estimate: **S** (small), **M** (medium), **L** (large). A TODO/FIXME scan of the codebase found none (clean). Cross-reference `IMPLEMENTATION_TASKS.md`.

Current milestone: **v0.1.0 "Foundation"** — DONE (pkg/logger, pkg/obsv, pkg/bootstrap; end-to-end cobra→wrapper→core works).
Next milestone: **v0.2.0 "Daemon"** (sub-project C, in progress).

---

## P1 — Next

### Implement sub-project C daemon gaps — **L**
Build out the sibling `inovacc/daemon` module's gaps so Mantle can run binaries as managed daemons. Scope:
- **Self-spawn guard** via `<APP>_DAEMON_CHILD` env to distinguish parent launcher from worker child.
- **Self-update** via `ExitUpgrade(4)` binary re-exec (`syscall.Exec` on Unix / process spawn on Windows).
- **Self-elevate** (UAC on Windows / sudo|polkit on Unix).
- **Persistent service** install via `kardianos/service`: install / uninstall / start / stop / status / run.

Tracked as milestone v0.2.0 "Daemon". Lifecycle hooks and bootstrap wiring follow in P2.

### Add cmd/logger tests (0% → covered) — **S**
The reference binary `logger-demo` (`cmd/logger`) currently has no tests (0% coverage). Add coverage proving the cobra→wrapper→core path: `App` squash-embeds `bootstrap.Base`, `RunE` calls `bootstrap.Run` with the core handler. Lifts the only untested package toward the 80% fleet target (overall across tested packages is ~85%).

---

## P2 — Soon

### Structured slog lifecycle hooks in daemon — **M**
Emit structured `log/slog` events for the daemon lifecycle: startup, restart (with backoff), loop-abort, worker-start, signal, shutdown, exit, and upgrade. Routes through the existing redacting logger handler chain. Depends on the P1 daemon gaps.

### Wire Features.Daemon / --daemon into bootstrap — **M**
Add a `Daemon` feature flag and a `--daemon` always-present persistent flag to `pkg/bootstrap`, evaluated alongside the existing `Features{Logging, Observability}` and overlaid like the other CHANGED-flag overrides. Activates daemon mode in the runtime. Depends on the P1 daemon gaps and the lifecycle hooks above.

### Module-path rename github.com/inovacc/logger → .../mantle — **M**
Mechanical rename of the Go module/import path to match the adopted brand. No external consumers yet, so this is low-risk but touches every import site, `go.mod` files, and docs. The brand "Mantle" is already adopted in docs ahead of the rename.

### Regex/content-based PII detection — **M**
Add value-based PII detection (regex/content heuristics) alongside the current struct-tag-only redaction (`pii:"redact"`, `pii:"mask"`, `pii:"hash"`, `pii:"-"`). Closes the gap where untagged fields carrying PII flow through unredacted.

### Resolve nested-container LogValuer redaction gap — **M**
A `slog.LogValuer` nested inside a container is not currently resolved or redacted (slog resolves `LogValuer` only at the top level). Implement recursive resolution so redaction reaches nested values.

### Auto-redact top-level interface-only structs — **M**
Top-level interface-only structs are not auto-redacted unless wrapped with `Safe(v)`. Detect and redact these automatically to remove the manual-wrap footgun. Related to the nested-container gap above.

---

## P3 — Later

### Config zero-dep adapter behind ConfigSource — **M**
Provide a stdlib-only `ConfigSource` implementation as an alternative to the `inovacc/config` (viper-backed) default. Removes the heavy config dependency for consumers who want a minimal footprint, reusing the existing `ConfigSource` seam (already used to inject test fakes). Note: `inovacc/config` is a process-global singleton (one config/process).

### Taskfile + CI + golangci-lint config — **S**
No `Taskfile`/`Makefile` exists yet. Add a `Taskfile.yml` (build / vet / `test -race` / coverage), a CI workflow, and a checked-in `golangci-lint` config per fleet convention.

### Widen redaction hash beyond 64-bit or document choice — **S**
The redaction hash is a 64-bit salted SHA-256 correlation token (16 hex), brute-forceable for low-entropy inputs. Either widen the output or document the correlation-token-not-cryptographic-secret rationale explicitly.

### TOML / dotenv config support — **S**
`inovacc/config` currently supports yaml/yml/json only. Add TOML and dotenv formats (also note: `default:` struct tags are ignored by the loader, which is why `DefaultBase()` applies defaults programmatically).

### First tagged release + goreleaser — **S**
Cut the first tagged release (v0.1.0 / v0.2.0) and add `goreleaser` for cross-platform artifacts. Greenfield on master, no git remote yet, conventional commits in use.

---

## Known limitations (documented in code)

These are real, intentional constraints — several map to backlog items above:
- Top-level interface-only structs not auto-redacted unless wrapped with `Safe` (see P2).
- No regex/content-based PII detection — struct-tag only (see P2).
- Redaction hash = 64-bit salted SHA-256 correlation token (brute-forceable for low-entropy inputs); mask reveals length (see P3).
- Map keys rendered with `%v` can collide for distinct non-string keys.
- Nested `slog.LogValuer` inside a container is not resolved/redacted (see P2).
- `inovacc/config` is a process-global singleton (one config/process), isolated behind `ConfigSource` for tests.
- `inovacc/config` supports yaml/yml/json only (no toml/dotenv); `default:` struct tags ignored (see P3).
- `cmd/logger` writes a default `config.yaml` in cwd when run without `--config` (loader auto-creates).
- Module-path rename to `github.com/inovacc/mantle` pending; brand adopted in docs first (see P2).

---

## Coverage snapshot (target 80%)

| Package | Coverage |
|---|---|
| `pkg/logger` | 81.5% |
| `pkg/obsv` | 86.6% |
| `pkg/bootstrap` | 91.3% |
| `cmd/logger` | 0% (no tests — see P1) |

Approx overall across tested packages: ~85%.
