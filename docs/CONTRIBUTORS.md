# Contributing to Mantle

Mantle (`github.com/inovacc/logger`) is a batteries-included Go application
runtime that wraps any binary — from its Cobra CLI to its core logic — with
PII-redacting structured logging, full OpenTelemetry observability, and
feature-flagged unified config. It is part of the inovacc fleet (config,
daemon, logger=Mantle).

- **Maintainer:** Dyam Marcano <dyam.marcano@gmail.com>
- **Go:** 1.25
- **License:** BSD-3-Clause
- **Repository:** greenfield on `master`, no git remote and no external CI yet.

> Note: the module path is still `github.com/inovacc/logger`. A rename to
> `github.com/inovacc/mantle` is a pending follow-up; the **Mantle** brand is
> adopted in docs first.

## Getting started

There is no Taskfile or Makefile yet. Use the Go toolchain directly:

```bash
# clone (no remote yet; clone from your local mirror or working copy)
git clone <your-mantle-checkout> mantle
cd mantle

go build ./...          # build every package
go vet ./...            # static checks
go test ./... -race     # full suite under the race detector
```

### Coverage

Target is **>= 80%** per package. Generate a profile with:

```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out      # per-function summary
go tool cover -html=coverage.out      # browse uncovered lines
```

Current coverage: `pkg/logger` 81.5%, `pkg/obsv` 86.6%, `pkg/bootstrap`
91.3%, `cmd/logger` 0% (the reference binary has no tests). Keep new code at
or above the target; do not regress a package below 80%.

Run `golangci-lint run --fix ./... --timeout=5m` before sending changes
(fleet convention).

## Project layout

| Package        | Role | Allowed dependency graph |
|----------------|------|--------------------------|
| `pkg/logger`   | Structured logging on `log/slog`; redact -> trace -> fanout handler chain; tag-driven PII redaction. | **stdlib + `go.opentelemetry.io/otel/trace` (API only) — NO SDK** |
| `pkg/obsv`     | Full OpenTelemetry bootstrap (logs/traces/metrics, OTLP + stdout exporters, propagators, resource detect). | Owns the **OTel SDK** deps |
| `pkg/bootstrap`| The cobra -> wrapper -> core runtime: flags, config load, feature eval, `Runtime` assembly. | Owns **cobra** + **inovacc/config** |
| `cmd/logger`   | Reference binary `logger-demo` proving cobra -> wrapper -> core. | depends on the above |

## Architecture invariants (do not break these)

1. **Redaction is outermost.** The redact handler wraps everything so no
   downstream sink ever sees raw PII.
2. **Import-graph purity.** `pkg/logger` must stay on stdlib + `otel/trace`
   only — never let it gain the OTel SDK, cobra, or inovacc/config. The SDK
   lives only in `pkg/obsv`; cobra + config live only in `pkg/bootstrap`.
   Enforced with `go list -deps` checks — run them when touching imports:

   ```bash
   go list -deps ./pkg/logger | grep -E 'otel/sdk|cobra|inovacc/config' && echo "PURITY VIOLATION" || echo OK
   ```
3. **Flags overlay manually.** inovacc/config exposes no public pflag binding;
   CHANGED flags are overlaid in a post-load pass (highest precedence) and
   defaults are applied programmatically via `DefaultBase` (the loader ignores
   `default:` struct tags).
4. **Composite config.** Users squash-embed `bootstrap.Base`
   (`mapstructure:",squash" yaml:",inline"`); don't flatten it.
5. **obsv before logger.** `obsv` is built first so its `LogSink` attaches to
   the logger fan-out; disabled subsystems yield no-op (never nil) Stacks so
   core code stays branch-free.

## Conventions

### Commits

Conventional commits, **prefixed by package**:

```
feat(obsv): add OTLP HTTP exporter gating
fix(logger): resolve mask keep=N off-by-one
test(bootstrap): cover fake ConfigSource precedence
docs(mantle): document redaction handler ordering
build: bump otelslog bridge to v0.7.0
chore: tidy go.mod
```

Allowed types: `feat`, `fix`, `test`, `docs`, `build`, `chore`. No
`Co-Authored-By` / AI attribution; use the configured git user.

### Tests

- **Table-driven** tests are the default style.
- Keep each package at **>= 80%** coverage.
- Run the suite under `-race` before sending changes.
- For config-dependent code, use the injectable fake `ConfigSource` rather
  than the inovacc/config process-global singleton.

### Platform-specific code

Split by OS only when needed, using build-constrained files:
`*_windows.go` / `*_unix.go` (e.g. the planned daemon self-update uses
`syscall.Exec` on Unix and a spawn on Windows). Ask before introducing a new
OS split.

### Running Go programs

Never compile-then-run. Use `go run`:

```bash
go run ./cmd/logger --log-level=debug --otel
```

The demo root command is `logger-demo`. Always-present persistent flags
(these override file + env): `-c/--config`, `--env`, `--log-level`,
`-v/--verbose`, `-q/--quiet`, `--log-format`, `--log-source`, `--no-redact`,
`--otel`, `--otel-endpoint`, `--otel-protocol`, `--version`.

## Breaking changes

For changes that could break consumers (config format, CLI flags, API shapes,
handler ordering): add the new behavior alongside the old, mark the old as
`// Deprecated:` with a removal date at least 30 days out, log deprecated
usage, provide a migration path, and track it in `docs/BACKLOG.md`. Remove
deprecated code only in a dedicated cleanup commit after the date.

## Roadmap context

Milestone **v0.1.0 "Foundation"** is done end-to-end (logger, obsv,
bootstrap; cobra -> wrapper -> core works). Active work is **v0.2.0 "Daemon"**:
daemon mode in the sibling `inovacc/daemon` module (self-spawn guard, self-update
via `ExitUpgrade(4)` re-exec, self-elevate, kardianos/service install) plus
structured lifecycle hooks, then wiring `Features.Daemon` / `--daemon` into
`pkg/bootstrap`. Contributions toward those gaps are welcome.

## Known limitations to keep in mind

- Top-level interface-only structs are not auto-redacted unless wrapped with
  `Safe`; nested `slog.LogValuer` inside a container is not resolved/redacted.
- Redaction is struct-tag only (no regex/content detection). The hash is a
  64-bit salted SHA-256 correlation token (brute-forceable for low-entropy
  inputs); `mask` reveals input length.
- Map keys rendered with `%v` can collide for distinct non-string keys.
- inovacc/config is a process-global singleton, supports yaml/yml/json only,
  and ignores `default:` tags (hence `DefaultBase`).
- `cmd/logger` writes a default `config.yaml` in the cwd when run without
  `--config`.

## License

By contributing you agree your contributions are licensed under the project's
**BSD-3-Clause** license.
