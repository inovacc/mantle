# Mantle — Known Limitations

These are documented, by-design limitations of **Mantle (github.com/inovacc/mantle)** as of
milestone **v0.1.0 "Foundation"**. Each entry is a **Limitation**, not a bug: the behavior is
intentional and understood. Where a workaround exists, it is noted inline.

> Scope note: Mantle is the brand. The Go module path is still `github.com/inovacc/mantle`;
> a rename to `github.com/inovacc/mantle` is a pending follow-up (see L9).

---

## L1 — Interface-only top-level structs are not auto-redacted

**Type:** Limitation (by design)

`slog` resolves `slog.LogValuer` only at the **top level** of an attribute value. A value passed
through an `any`/interface field at the top of a log call does not get its `pii:` struct tags
applied automatically by the redaction handler.

**Workaround:** Wrap the value with `logger.Safe(v)` before logging. `Safe` returns a deferred
`slog.LogValuer` that the redaction pipeline resolves and redacts correctly.

```go
log.Info("user", "data", logger.Safe(user))
```

---

## L2 — No regex / content-based PII detection

**Type:** Limitation (by design)

Redaction is **struct-tag driven only** (`pii:"redact"`, `pii:"mask"`, `pii:"hash"`,
`pii:"-"`/`omit`/`drop`). Mantle does **not** scan string contents for PII patterns (emails,
card numbers, etc.). A sensitive value placed in a field with no `pii:` tag is logged as-is.

**Workaround:** Annotate sensitive fields with the appropriate `pii:` tag. There is no automatic
content inspection; correct tagging is the sole mechanism.

---

## L3 — Hash is a 64-bit correlation token, and mask reveals length

**Type:** Limitation (by design)

`pii:"hash"` emits a **salted SHA-256 truncated to 16 hex chars (64 bits)**. This is a
**correlation token**, not a cryptographic commitment — for low-entropy inputs it is
brute-forceable by an attacker who knows the salt and the candidate set. Likewise, `pii:"mask"`
(reveal last N, default 4) intentionally **reveals the original value's length**.

**Workaround:** None — this is the intended trade-off (correlate-without-storing-cleartext). For
truly sensitive low-entropy fields, prefer `pii:"redact"` (`[REDACTED]`) or `pii:"-"`/omit so no
correlatable token or length is emitted. Set a strong, secret salt via `SetHashSalt` /
`Config.HashSalt`.

---

## L4 — Map keys rendered with `%v` can collide for non-string keys

**Type:** Limitation (by design)

When a map is rendered, keys are stringified with `%v`. Distinct **non-string** keys that share
the same `%v` representation will collide into the same rendered key in the log output.

**Workaround:** Use string keys for maps that will be logged, or pre-flatten the map into
explicit attributes before logging.

---

## L5 — Nested `slog.LogValuer` inside a container is not resolved/redacted

**Type:** Limitation (by design)

Same root cause as L1: `slog` resolves `LogValuer` only at the top level. A `LogValuer` (including
a `Safe`-wrapped value) nested **inside** a container — a slice, map, or struct field — is **not**
resolved, so its `pii:` tags are not applied.

**Workaround:** Resolve/wrap at the top level. Pass each sensitive element as its own
`logger.Safe(...)` attribute rather than relying on redaction to reach into a container.

---

## L6 — `inovacc/config` is a process-global singleton

**Type:** Limitation (upstream dependency)

The underlying `inovacc/config` loader is a **process-global singleton**: one configuration per
process. This makes parallel/isolated configuration awkward, especially in tests.

**Workaround:** Mantle isolates this behind the `bootstrap.ConfigSource` interface. Tests inject a
fake `ConfigSource` instead of touching the global singleton, so they remain hermetic.

---

## L7 — Config supports yaml/yml/json only, and `default:` tags are ignored

**Type:** Limitation (upstream dependency)

`inovacc/config` supports **yaml/yml/json only** — no TOML, no dotenv. It also **ignores
`default:` struct tags**, so zero-value fields are not auto-populated from tags.

**Workaround:** Defaults are applied **programmatically** via `bootstrap.DefaultBase()` rather
than through `default:` tags. Use yaml/yml/json for config files; for other formats, convert
first.

---

## L8 — `cmd/logger` auto-creates `config.yaml` in the working directory

**Type:** Limitation (by design)

The reference binary `logger-demo` (`cmd/logger`) writes a default `config.yaml` into the current
working directory when run **without** `--config`. The loader auto-creates the file on first run.

**Workaround:** Pass `-c`/`--config <path>` to point at an explicit config file and avoid the
implicit write into the cwd.

---

## L9 — Module-path rename to `mantle` is pending

**Type:** Limitation (planned follow-up)

The brand is **Mantle**, but the Go module / import path is still `github.com/inovacc/mantle`.
The rename to `github.com/inovacc/mantle` has not yet landed; the brand is adopted in docs first.

**Workaround:** Import using the current path `github.com/inovacc/mantle`. Track the rename as a
pending follow-up before depending on the `mantle` path.

---

## Summary

| ID | Limitation | Workaround |
|----|------------|------------|
| L1 | Interface-only top-level structs not auto-redacted | Wrap with `logger.Safe` |
| L2 | No regex/content PII detection (struct tags only) | Annotate fields with `pii:` tags |
| L3 | 64-bit hash brute-forceable; mask reveals length | By design; use `redact`/omit for sensitive low-entropy fields |
| L4 | Map `%v` key collision for non-string keys | Use string keys / pre-flatten |
| L5 | Nested `LogValuer`-in-container not resolved | Wrap/resolve at top level |
| L6 | `inovacc/config` global singleton | Isolated behind `ConfigSource` for tests |
| L7 | yaml/yml/json only; `default:` tags ignored | Defaults via `DefaultBase` |
| L8 | `cmd/logger` auto-creates `config.yaml` in cwd | Pass `--config` |
| L9 | Module rename to `mantle` pending | Import `github.com/inovacc/mantle` for now |
