package logger

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// capture is a test slog.Handler that records the attrs of the last record.
type capture struct {
	attrs   map[string]slog.Value
	err     error
	enabled bool
}

func newCapture() *capture { return &capture{attrs: map[string]slog.Value{}, enabled: true} }

func (c *capture) Enabled(context.Context, slog.Level) bool { return c.enabled }
func (c *capture) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool { c.attrs[a.Key] = a.Value.Resolve(); return true })
	return c.err
}
func (c *capture) WithAttrs(a []slog.Attr) slog.Handler {
	for _, at := range a {
		c.attrs[at.Key] = at.Value.Resolve()
	}
	return c
}
func (c *capture) WithGroup(string) slog.Handler { return c }

func record(attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time_Now(), slog.LevelInfo, "msg", 0)
	r.AddAttrs(attrs...)
	return r
}

func TestRedactHandlerScrubsBeforeNext(t *testing.T) {
	cap := newCapture()
	h := redactHandler{next: cap, r: NewRedactor("s")}
	if err := h.Handle(context.Background(), record(slog.Any("user", sampleUser()))); err != nil {
		t.Fatal(err)
	}
	grp := cap.attrs["user"]
	if grp.Kind() != slog.KindGroup {
		t.Fatalf("user not a group: %v", grp.Kind())
	}
	m := valueToAny(grp).(map[string]any)
	if m["ssn"] != "[REDACTED]" {
		t.Errorf("next handler saw raw ssn: %v", m["ssn"])
	}
}

func TestTraceHandlerInjectsIDs(t *testing.T) {
	cap := newCapture()
	h := traceHandler{next: cap}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0xc, 0xd, 0xe, 0xf, 0x10},
		SpanID:     trace.SpanID{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	if err := h.Handle(ctx, record(slog.String("k", "v"))); err != nil {
		t.Fatal(err)
	}
	if _, ok := cap.attrs["trace_id"]; !ok {
		t.Error("trace_id not injected")
	}
	if _, ok := cap.attrs["span_id"]; !ok {
		t.Error("span_id not injected")
	}
}

func TestTraceHandlerNoSpanNoIDs(t *testing.T) {
	cap := newCapture()
	h := traceHandler{next: cap}
	if err := h.Handle(context.Background(), record(slog.String("k", "v"))); err != nil {
		t.Fatal(err)
	}
	if _, ok := cap.attrs["trace_id"]; ok {
		t.Error("trace_id injected without a span")
	}
}

func TestFanoutDispatchesToAll(t *testing.T) {
	a, b := newCapture(), newCapture()
	h := fanoutHandler{handlers: []slog.Handler{a, b}}
	if err := h.Handle(context.Background(), record(slog.String("k", "v"))); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.attrs["k"]; !ok {
		t.Error("sink A missed record")
	}
	if _, ok := b.attrs["k"]; !ok {
		t.Error("sink B missed record")
	}
}

func TestFanoutJoinsErrors(t *testing.T) {
	a := newCapture()
	b := newCapture()
	b.err = errors.New("boom")
	h := fanoutHandler{handlers: []slog.Handler{a, b}}
	err := h.Handle(context.Background(), record(slog.String("k", "v")))
	if err == nil || err.Error() != "boom" {
		t.Errorf("expected joined error, got %v", err)
	}
	if _, ok := a.attrs["k"]; !ok {
		t.Error("a failing sink suppressed a healthy one")
	}
}

func time_Now() time.Time { return time.Unix(1700000000, 0).UTC() }
