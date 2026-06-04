# 2. PII Redaction as the Outermost slog Handler

- Status: Accepted
- Date: 2026-06-04
- Project: Mantle (github.com/inovacc/logger)
- Maintainer: Dyam Marcano <dyam.marcano@gmail.com>

## Context

Mantle wraps any Go binary with PII-redacting structured logging built on
`log/slog`. A single log record can fan out to multiple destinations: the local
JSON/Text sink and any number of extra sinks attached via `logger.WithSink` —
most importantly the OpenTelemetry log sink supplied by `pkg/obsv` (`Stack.LogSink()`).

This creates a hard requirement: **no sink may ever observe raw PII.** If
redaction ran per-sink, or anywhere downstream, a single misconfigured or
late-attached sink could exfiltrate unredacted fields to an OTLP collector or
stdout. Redaction therefore cannot be a property of a sink — it must be a
property of the pipeline that feeds every sink.

A second constraint is cost. Most log lines carry untagged, ordinary types, and
many records are dropped by level filtering before they are ever formatted.
Redaction must add near-zero overhead for these cases: it must not pay a
reflection tax on every attribute, and it must do no work at all on lines that
the level filter discards.

The handler chain (outermost -> innermost) is:

    redactHandler -> traceHandler -> fanoutHandler -> [JSON/Text local sink, extra WithSink sinks]

## Decision

Implement tag-driven PII redaction as the **outermost** `slog.Handler` in the
chain, ahead of trace correlation, fan-out, and all sinks.

Redaction is governed entirely by struct tags on the logged types:

- `pii:"redact"` -> value replaced with `[REDACTED]`
- `pii:"mask"` / `pii:"mask,keep=N"` -> reveal the last N characters (default 4)
- `pii:"hash"` -> salted SHA-256, rendered as a 16-hex (64-bit) correlation token
- `pii:"-"` / `omit` / `drop` -> field omitted; `json:"-"` is also omitted

The implementation uses a **single reflection walker** with a **unified
per-type cache**. The first time the walker encounters a concrete type it
computes and memoises that type's redaction plan (which fields to redact, mask,
hash, or omit). Subsequent records of the same type reuse the cached plan, so a
type with no PII tags resolves to a cached "nothing to do" verdict and is passed
through untouched.

For values that `slog` will not descend into automatically, `Safe(v)` returns a
deferred `slog.LogValuer`. Because redaction sits at the top of the chain and
`Safe` resolves lazily, the walker runs once, at emit time, on the outermost
handler — never repeatedly down the stack.

Redaction default is on (`Config.Redact = true`); it is opt-out via the
`--no-redact` flag. The hash salt is configurable (`Config.HashSalt`,
`SetHashSalt`). Custom behaviour is available through `NewRedactor` /
`WithRedactor` / `WithReplaceAttr`.

## Consequences

### Positive

- **No sink ever sees raw PII.** Because redaction is the outermost handler,
  every downstream consumer — local JSON/Text, every `WithSink`, and the
  `pkg/obsv` OTel log sink — receives only already-redacted attributes. Adding a
  new sink cannot reopen a leak.
- **Near-zero overhead for untagged types.** The per-type cache means an
  untagged type is walked once and thereafter recognised as a no-op. Combined
  with `slog` level filtering happening at the same outermost layer, dropped
  lines cost effectively nothing.
- **Single source of truth.** One walker and one cache mean there is exactly one
  place where the redaction policy is interpreted, keeping behaviour consistent
  across all sinks and formats.
- **Branch-free core.** Core application code logs normally; whether redaction,
  tracing, or OTel are active is invisible to it.

### Negative / Trade-offs

- **Reflection- and tag-based only.** Redaction is driven by struct tags walked
  via reflection. There is **no regex or content-based PII detection** — an
  untagged string containing an email or card number is logged verbatim.
- **Hash is a correlation token, not a secret.** `pii:"hash"` produces a 64-bit
  salted SHA-256 value intended for correlating records, not for hiding values;
  it is brute-forceable for low-entropy inputs. `mask` similarly reveals the
  length of the original value.
- **Top-level LogValuer resolution.** `slog` resolves `LogValuer` only at the
  top level, so interface-only structs are not auto-redacted unless wrapped with
  `Safe(v)`, and a nested `slog.LogValuer` inside a container is not resolved or
  redacted.
- **Map-key rendering.** Map keys rendered with `%v` can collide for distinct
  non-string keys.

## Alternatives Considered

- **Per-sink redaction.** Rejected: it duplicates policy, scales poorly with
  sink count, and any new or misconfigured sink becomes a leak vector.
- **Innermost redaction (just before formatting).** Rejected: trace and fan-out
  handlers would handle raw PII, and each sink would still need to redact
  independently.
- **Regex/content scanning.** Rejected for v0.1.0: high false-positive/negative
  rate and per-record cost on every attribute conflict with the near-zero
  overhead goal. The struct-tag model keeps cost on the type, not the value.

## Related

- ADR 0001 (handler-chain ordering) — establishes redact -> trace -> fanout -> sinks.
- Architecture invariant 1: the redaction handler is outermost so no downstream
  sink ever sees raw PII.
- `pkg/logger` (coverage 81.5%); OTel log sink supplied by `pkg/obsv`
  (`Stack.LogSink()`).
