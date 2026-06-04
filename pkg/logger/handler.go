package logger

import (
	"context"
	"errors"
	"log/slog"
	"reflect"

	"go.opentelemetry.io/otel/trace"
)

// redactHandler redacts any attribute that is (or contains) a struct carrying a
// pii tag, before the record reaches the next handler. It is the outermost
// handler so every downstream sink only ever sees redacted data.
type redactHandler struct {
	next slog.Handler
	r    *Redactor
}

func (h redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h redactHandler) redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve() // collapse any LogValuer (incl. Safe) first
	switch v.Kind() {
	case slog.KindGroup:
		grp := v.Group()
		red := make([]slog.Attr, 0, len(grp))
		for _, ga := range grp {
			red = append(red, h.redactAttr(ga))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(red...)}
	case slog.KindAny:
		raw := v.Any()
		if raw != nil && infoFor(reflect.TypeOf(raw)).hasPII {
			return slog.Attr{Key: a.Key, Value: h.r.Value(reflect.ValueOf(raw), 0)}
		}
		return slog.Attr{Key: a.Key, Value: v}
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}

func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = h.redactAttr(a)
	}
	return redactHandler{next: h.next.WithAttrs(red), r: h.r}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{next: h.next.WithGroup(name), r: h.r}
}

// traceHandler enriches every record with the active OpenTelemetry trace and
// span IDs so logs can be correlated with traces in your backend.
type traceHandler struct{ next slog.Handler }

func (h traceHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h traceHandler) Handle(ctx context.Context, rec slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec = rec.Clone()
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, rec)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{next: h.next.WithGroup(name)}
}

// fanoutHandler dispatches each record to every wrapped handler, so logs can go
// to a local sink and an OTel bridge simultaneously.
type fanoutHandler struct{ handlers []slog.Handler }

func (h fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, hh := range h.handlers {
		if hh.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, rec slog.Record) error {
	var errs error
	for _, hh := range h.handlers {
		if hh.Enabled(ctx, rec.Level) {
			if err := hh.Handle(ctx, rec.Clone()); err != nil {
				errs = errors.Join(errs, err)
			}
		}
	}
	return errs
}

func (h fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, hh := range h.handlers {
		next[i] = hh.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: next}
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, hh := range h.handlers {
		next[i] = hh.WithGroup(name)
	}
	return fanoutHandler{handlers: next}
}
