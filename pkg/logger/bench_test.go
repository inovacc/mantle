package logger

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
)

var sinkValue slog.Value

func BenchmarkRedactValueNested(b *testing.B) {
	rd := NewRedactor("salt")
	rv := reflect.ValueOf(sampleUser())
	b.ReportAllocs()
	for b.Loop() {
		sinkValue = rd.Value(rv, 0)
	}
}

func BenchmarkLoggerRedactJSON(b *testing.B) {
	lg, err := New(Config{Output: io.Discard, Redact: true, HashSalt: "s"})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	u := sampleUser()
	b.ReportAllocs()
	for b.Loop() {
		lg.LogAttrs(ctx, slog.LevelInfo, "signup", slog.Any("user", u))
	}
}

// Untagged struct must not pay redaction cost: compare redact-on logger vs plain.
func BenchmarkLoggerPlainStructRedactOn(b *testing.B) {
	type plain struct {
		A string `json:"a"`
		N int    `json:"n"`
	}
	lg, _ := New(Config{Output: io.Discard, Redact: true})
	ctx := context.Background()
	p := plain{A: "x", N: 1}
	b.ReportAllocs()
	for b.Loop() {
		lg.LogAttrs(ctx, slog.LevelInfo, "evt", slog.Any("p", p))
	}
}

// Below-threshold lines must be nearly free with Safe (deferred LogValuer).
func BenchmarkLoggerSafeFilteredOut(b *testing.B) {
	lg, _ := New(Config{Output: io.Discard, Level: "info"})
	ctx := context.Background()
	u := sampleUser()
	b.ReportAllocs()
	for b.Loop() {
		lg.LogAttrs(ctx, slog.LevelDebug, "trace", slog.Any("user", Safe(u)))
	}
}
