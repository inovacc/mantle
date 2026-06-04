package obsv

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inovacc/mantle/logger"
)

// safeWriter is a concurrency-safe sink for the stdout exporters' background
// goroutines (so -race stays clean when multiple signals share one writer).
type safeWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *safeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
func (w *safeWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.String() }
func (w *safeWriter) Len() int       { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.Len() }

func TestDisabledStackNoops(t *testing.T) {
	st, err := New(context.Background(), Config{Enabled: false}, ServiceInfo{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if st.LogSink() != nil {
		t.Error("disabled LogSink should be nil")
	}
	_, span := st.Tracer("t").Start(context.Background(), "op")
	span.End()
	_ = st.Meter("t")
	if err := st.Shutdown(context.Background()); err != nil {
		t.Errorf("disabled Shutdown should be nil, got %v", err)
	}
}

func TestEnabledStdoutBuilds(t *testing.T) {
	w := &safeWriter{}
	st, err := New(context.Background(), Config{Enabled: true}, ServiceInfo{Name: "svc", Version: "1.0"}, WithStdoutWriter(w))
	if err != nil {
		t.Fatal(err)
	}
	if st.LogSink() == nil {
		t.Error("enabled LogSink should be non-nil")
	}
	_, span := st.Tracer("t").Start(context.Background(), "op")
	span.End()
	if err := st.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if w.Len() == 0 {
		t.Error("expected stdout exporter to emit something after shutdown")
	}
}

func TestLogSinkEmitsThroughBridge(t *testing.T) {
	w := &safeWriter{}
	st, err := New(context.Background(), Config{Enabled: true, Signals: Signals{Logs: true}}, ServiceInfo{Name: "svc"}, WithStdoutWriter(w))
	if err != nil {
		t.Fatal(err)
	}
	lg, err := logger.New(logger.Config{Output: io.Discard, Redact: true, HashSalt: "s"}, logger.WithSink(st.LogSink()))
	if err != nil {
		t.Fatal(err)
	}
	type acct struct {
		User string `json:"user"`
		SSN  string `json:"ssn" pii:"redact"`
	}
	lg.LogAttrs(context.Background(), slog.LevelInfo, "signup", slog.Any("acct", acct{User: "ada", SSN: "123-45-6789"}))
	if err := st.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if !strings.Contains(out, "signup") {
		t.Errorf("bridge did not emit message: %q", out)
	}
	if strings.Contains(out, "123-45-6789") {
		t.Errorf("raw SSN leaked to OTel sink: %q", out)
	}
}

func TestPerSignalGatingLogsOnly(t *testing.T) {
	w := &safeWriter{}
	st, err := New(context.Background(), Config{Enabled: true, Signals: Signals{Logs: true}}, ServiceInfo{Name: "svc"}, WithStdoutWriter(w))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Shutdown(context.Background())
	if st.LogSink() == nil {
		t.Error("logs enabled → LogSink non-nil")
	}
	_, span := st.Tracer("t").Start(context.Background(), "op")
	span.End()
	_ = st.Meter("t")
}

func TestInvalidProtocolErrors(t *testing.T) {
	_, err := New(context.Background(), Config{Enabled: true, Endpoint: "localhost:4317", Protocol: "xml"}, ServiceInfo{Name: "t"})
	if err == nil {
		t.Error("expected error for invalid protocol")
	}
}

func TestOTLPExportersBuild(t *testing.T) {
	for _, proto := range []string{ProtocolGRPC, ProtocolHTTP} {
		st, err := New(context.Background(), Config{Enabled: true, Endpoint: "localhost:4317", Protocol: proto, Insecure: true}, ServiceInfo{Name: "t"})
		if err != nil {
			t.Fatalf("%s: New: %v", proto, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = st.Shutdown(ctx)
		cancel()
	}
}

func TestShutdownIdempotent(t *testing.T) {
	w := &safeWriter{}
	st, _ := New(context.Background(), Config{Enabled: true, Signals: Signals{Traces: true}}, ServiceInfo{Name: "t"}, WithStdoutWriter(w))
	if err := st.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown should be safe, got %v", err)
	}
}
