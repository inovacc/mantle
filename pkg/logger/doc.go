// Package logger provides an opinionated structured-logging package built on the
// standard library's log/slog.
//
// It gives client code three things:
//
//   - A configured *slog.Logger writing JSON or text to a local sink, with extra
//     fan-out sinks pluggable via WithSink (e.g. an OpenTelemetry bridge from
//     github.com/inovacc/mantle/pkg/obsv).
//   - Automatic correlation of every record with the active trace
//     (trace_id / span_id) when the context carries a valid span.
//   - PII redaction driven by struct tags. Tag a field with `pii:"..."` and the
//     value is redacted, masked, hashed, or omitted before it leaves the process.
//
// Tag reference:
//
//	pii:"redact"      // value replaced with [REDACTED] (also: pii:"true")
//	pii:"mask"        // reveal trailing chars, star the rest (default keep 4)
//	pii:"mask,2"      // ...keep last 2 (also: pii:"mask,keep=6")
//	pii:"hash"        // salted SHA-256 digest (stable, non-reversible)
//	pii:"-"           // field omitted entirely (also: omit, drop)
//
// A field tagged `json:"-"` is also omitted from logs, so values excluded from
// API responses do not silently leak.
//
// This package depends only on the standard library and the OpenTelemetry trace
// API (go.opentelemetry.io/otel/trace); it does not import the OTel SDK.
package logger
