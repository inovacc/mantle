# ADR-0003: Mantle — Config Provider Behind a `ConfigSource` Interface

## Status

Accepted

## Context

Mantle (`github.com/inovacc/logger`) is part of the inovacc fleet (config, daemon,
logger=Mantle). The `pkg/bootstrap` runtime needs to load application configuration
from defaults, a file, and the environment, then expose it as a composite
`bootstrap.Base` that any consuming app can squash-embed.

The fleet standard for configuration is the published, viper-backed
`github.com/inovacc/config` module. The sibling `inovacc/daemon` module already
depends on `v1.2.2`, so adopting the same version in Mantle keeps the fleet on a
single, shared config dependency and avoids divergent loading semantics across
modules.

However, `inovacc/config` carries three properties that constrain how it can be
embedded in `pkg/bootstrap`:

1. **Process-global singleton.** It holds one config per process. Loading it
   directly inside `bootstrap.Configure` would make the runtime impossible to test
   in isolation (parallel tests would stomp the shared global) and would couple the
   bootstrap package to a global lifecycle.
2. **No public pflag binding.** It exposes no API to bind the 11 always-present
   persistent CLI flags (`-c/--config`, `--env`, `--log-level`, `-v/--verbose`,
   `-q/--quiet`, `--log-format`, `--log-source`, `--no-redact`, `--otel`,
   `--otel-endpoint`, `--otel-protocol`, `--version`) into the loaded config, so
   flag precedence cannot be delegated to the loader.
3. **Ignores `default:` struct tags.** The loader does not apply `default:` tags, so
   zero-value fields stay zero unless defaults are supplied another way.

It also supports only yaml/yml/json (no toml/dotenv). Architecture invariant #2
requires that cobra and `inovacc/config` live ONLY in the `pkg/bootstrap` import
graph — they must not leak into `pkg/logger` (stdlib + otel/trace API only) or
`pkg/obsv` (OTel SDK only).

## Decision

Integrate `inovacc/config v1.2.2` behind a small `bootstrap.ConfigSource` interface
rather than calling the loader directly, and compose configuration as follows:

- **`ConfigSource` interface** wraps the load step. The default implementation is the
  viper-backed `inovacc/config v1.2.2` loader; a fake implementation is injectable for
  tests. This isolates the process-global singleton behind a seam the runtime owns.
- **Composite config via squash-embedding.** Consuming apps squash-embed
  `bootstrap.Base` (`mapstructure:",squash" yaml:",inline"`). `Base` aggregates
  `Environment`, `Features`, `logger.Config`, and `obsv.Config`. The unexported
  `base()` method on the `Configurable` interface means only types that embed `Base`
  satisfy it (Service-any composition).
- **Programmatic defaults via `DefaultBase()`.** Because the loader ignores `default:`
  tags, defaults are applied programmatically through `DefaultBase()` rather than
  declared as struct tags.
- **Manual flag overlay post-load.** Because the loader exposes no pflag binding, the
  `PersistentPreRunE` overlays only the CHANGED cobra flags onto the loaded config in a
  manual post-load pass. Resulting precedence is: defaults → file → env → CHANGED flags
  (highest).

The full load order inside `PersistentPreRunE`: `DefaultBase()` programmatic defaults →
`ConfigSource` load (file then env) → manual CHANGED-flag overlay → evaluate `Features`
→ build the `Runtime{Cfg, Logger, Tracer, Meter, Shutdown}` and store it in the command
context.

## Consequences

### Positive

- **Fleet-consistent.** Mantle and `inovacc/daemon` share the same config dependency at
  the same version (`v1.2.2`), keeping loading semantics uniform across the inovacc
  fleet.
- **Service-any composition.** Squash-embedding `bootstrap.Base` lets any app compose its
  own config on top of the runtime's, satisfied via the unexported `base()` method.
- **Testable despite a global singleton.** `ConfigSource` isolates the process-global
  singleton; tests inject a fake loader and run without touching (or racing on) the real
  global. This is reflected in `pkg/bootstrap` coverage of 91.3%.
- **Swappable loader.** Replacing the viper-backed loader with a zero-dependency loader
  (e.g. to drop the heavy viper/`inovacc/config` graph, or to add toml/dotenv support
  the current loader lacks) becomes a one-adapter change behind `ConfigSource` — no
  changes to call sites.
- **Import-graph purity preserved.** cobra and `inovacc/config` stay confined to the
  `pkg/bootstrap` graph (invariant #2), enforced by `go list -deps` checks.

### Negative

- **Defaults must be maintained programmatically.** `DefaultBase()` is the single source
  of truth for defaults; `default:` struct tags are inert and must not be relied upon,
  which is easy to forget when adding new fields.
- **Flags overlaid manually post-load.** The CHANGED-flag overlay is hand-written rather
  than delegated to the loader, so adding a new always-present flag requires updating the
  overlay pass as well as flag registration.
- **One config per process.** The underlying singleton means a single config per process;
  multi-config scenarios are only achievable through the `ConfigSource` seam, not the
  default loader.
- **Format limits inherited.** yaml/yml/json only — no toml/dotenv until the loader is
  swapped.

## Alternatives Considered

- **Call `inovacc/config` directly in `bootstrap.Configure`.** Rejected: leaks the
  process-global singleton into the runtime, makes parallel tests unsafe, and hard-wires
  the fleet to viper with no swap path.
- **Build a bespoke zero-dependency loader now.** Rejected for v0.1.0: diverges from the
  fleet standard that `inovacc/daemon` already uses and duplicates work. The
  `ConfigSource` seam keeps this option open as a future one-adapter swap.
- **Rely on `default:` struct tags for defaults.** Not viable: the `inovacc/config`
  loader ignores them, so defaults would silently be zero values. Hence `DefaultBase()`.
- **Wait for upstream pflag binding to handle flag precedence.** Rejected: no public API
  exists today; the manual post-load overlay is deterministic and does not block on
  upstream.

## Related

- ADR-0001 — handler-chain ordering (redaction outermost) in `pkg/logger`.
- ADR-0002 — observability built before the logger so `obsv.LogSink()` attaches to the
  fan-out; disabled subsystems yield no-op (never nil) stacks.
- Architecture invariants #2 (import-graph purity), #3 (manual flag overlay,
  programmatic defaults), and #4 (composite config via squash-embedded `Base`).
