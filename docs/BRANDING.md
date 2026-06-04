# logger — Branding Names

> Branding reference for `github.com/inovacc/mantle`. The module-path rename from `github.com/inovacc/logger` to `github.com/inovacc/mantle` is done. Part of the **inovacc** fleet (`config`, `daemon`, `mantle`).

## Project Identity

- **Current name:** `mantle` (`github.com/inovacc/mantle`)
- **Core purpose (one sentence):** A batteries-included Go application runtime that wraps any binary — from its Cobra CLI to its core logic — with PII-redacting structured logging, full OpenTelemetry observability, and feature-flagged unified config.
- **Key features:** tag-driven PII redaction (`redact`/`mask`/`hash`/`omit`) · `Safe()` deferred redaction · trace-ID correlation · full OTel bootstrap (logs + traces + metrics over OTLP, runtime metrics) · config-driven feature flags · `cobra → wrapper → core-app` runtime · always-present CLI flags · composite (embeddable) config.
- **Target audience:** Go backend developers and platform / ops engineers shipping production services.
- **Technical domain:** observability · structured logging · privacy / PII redaction · application-runtime bootstrap · CLI.

The name *logger* is accurate but **undersells the scope** — this is an observability-and-privacy runtime, not just a log writer. The candidates below lean into that.

## Project Name Candidates

| # | Name | Rationale |
|---|------|-----------|
| 1 | **logger** *(current)* | Honest and discoverable, but reads as "just a log library" and hides the observability + redaction + runtime story. |
| 2 | **Prism** ⭐ *recommended* | One bootstrap splits a single stream into the three OTel signals (logs, traces, metrics) — like a prism splits light. Also evokes *clarity / seeing through*, in tension with the opacity of redaction. Brandable, on-domain. |
| 3 | **Veil** *(runner-up)* | Privacy-forward: PII is veiled (redacted/masked/hashed) before it ever leaves the process. Short, memorable, distinctive. |
| 4 | **Mantle** *(runner-up)* | The protective layer you wrap your core app in — the literal `wrapper` that cloaks and instruments your binary. |
| 5 | **Aegis** | A shield that guards PII while observability keeps watch — protection + vigilance in one word. |
| 6 | **Halo** | A telemetry-and-protection aura surrounding your app; the wrapper that rings your core logic. |
| 7 | **Lumen** | Light / visibility (observability) in a compact, brandable form. |
| 8 | **Beacon** | Emits signals (logs/traces/metrics) and lights the way for operators. |
| 9 | **Cloak** | Privacy metaphor — automatically cloaks sensitive fields; pairs naturally with "wrapper". |
| 10 | **Loom** | Weaves the three telemetry threads and log lines into one fabric. |
| 11 | **Scribe** | The diligent, trustworthy recorder — logging-forward with a human feel. |
| 12 | **Warden** | Guards data (redaction), watches the system (observability), supervises the process (daemon). |

**Recommendation:** **Prism** as the headline brand (captures the "one call → three signals" observability superpower and reads clean as `inovacc/prism`), with **Veil** if you want privacy/redaction to lead, or **Mantle** if you want the runtime-wrapper idea to lead.

## Feature Names

| Feature | Current Name | Branded Name Options |
|---------|--------------|----------------------|
| PII redaction engine | `Redactor` / `redact.go` | Veil · Obscura · Scrub |
| Deferred-redaction wrapper | `Safe()` | Shroud() · Guard() · Cloak() |
| Trace correlation | `traceHandler` | Tether · Correlate · Threadlink |
| OTel bootstrap | `pkg/obsv` | Prism · Spectrum · Aperture |
| Config feature flags | `Features` | Switchboard · Gates · Toggles |
| Cobra → wrapper → core runtime | `pkg/bootstrap` | Mantle · Harness · Conduit |
| Always-present CLI flags | bootstrap flags | Baseline · Coreflags · Standard-issue |
| Handler fan-out | `fanoutHandler` | Manifold · Splitter · Relay |

## Component Names

| Component | Branded Name Options |
|-----------|----------------------|
| `pkg/logger` | Quill · Scribe · Veilog |
| `pkg/obsv` | Prism · Spectrum · Aperture |
| `pkg/bootstrap` | Mantle · Harness · Ignition |
| `Redactor` | Veil · Obscura · Scrubber |
| `Stack` (obsv) | Spectrum · Lattice · Rig |
| `Runtime` (bootstrap) | Harness · Chassis · Core |
| `LogSink` bridge | Conduit · Tap · Span |
| handler chain | Pipeline · Manifold · Relay |

## Taglines

- **See everything. Leak nothing.**
- Structured logs, redacted by default.
- From CLI to core, fully observable.
- One wrapper — logs, traces, metrics.
- Privacy-safe telemetry for Go services.
- Wrap your binary in observability.
- `slog` with superpowers.
- Redact, correlate, export — automatically.

## CLI Branding Themes

**Theme A — Observatory** (observability metaphor)
```
<app> watch              # run the instrumented service
<app> lens               # print the resolved config
<app> beacon             # version / build info
<app> orbit up           # install + start the service
<app> orbit down         # stop + uninstall
<app> orbit status       # service status
```

**Theme B — Infrastructure** (plumbing metaphor)
```
<app> run                # serve
<app> wiring             # show resolved config
<app> build              # version
<app> service up|down|status
```

**Theme C — Minimal** (plain, inovacc-fleet-consistent)
```
<app> serve
<app> config
<app> version
<app> service start|stop|status
```

## Color Palette Suggestions

| Role | Name | Hex |
|------|------|-----|
| Primary | Indigo | `#4F46E5` |
| Secondary | Violet | `#7C3AED` |
| Accent | Amber | `#F59E0B` |
| Warning | Rose | `#E11D48` |
| Muted | Slate | `#64748B` |

The indigo→violet gradient reads as a spectrum (fitting **Prism**); amber accents highlight CTAs and active state; rose flags conflicts/errors; slate carries secondary text and borders.

## Logo Concepts

1. **Prism refraction** — a triangular prism splitting one white beam into three colored rays (logs / traces / metrics); the literal mark for the "one source → three signals" story.
2. **Eye + redaction bar** — a stylized eye whose pupil is a solid redaction bar: *see everything, hide the sensitive bits*.
3. **Wrapping brackets** — concentric `[ ]` brackets enclosing a small gradient glyph: the *mantle / wrapper* around your core app.
4. **Signal-bar shield** — three stacked telemetry bars whose silhouette forms a shield: protection (redaction) meets telemetry (observability).

### Brand Icon (IconForge)

Generated with the palette above under the recommended name **prism**:

```bash
iconforge forge --generate \
  --name prism \
  --primary "#4F46E5" \
  --secondary "#7C3AED" \
  --accent "#F59E0B" \
  --output build/icons
```

Output (if generated): `build/icons/` → `prism.svg`, `png/*`, `windows/*`, `macos/*`, `linux/*`.
