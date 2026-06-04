package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// logToMap builds a logger that writes JSON to a buffer, emits one record with
// the given attr, unmarshals the JSON object, and returns the "user" sub-map.
func logToMap(t *testing.T, cfg Config, attr slog.Attr, opts ...Option) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	cfg.Output = &buf
	cfg.Format = FormatJSON
	lg, err := New(cfg, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lg.LogAttrs(context.Background(), slog.LevelInfo, "test", attr)
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("unmarshal %q: %v", buf.String(), err)
	}
	u, ok := rec["user"].(map[string]any)
	if !ok {
		t.Fatalf("user field missing/typed wrong: %v", rec["user"])
	}
	return u
}

// assertRedacted checks that PII fields in u were processed and non-PII left intact.
func assertRedacted(t *testing.T, u map[string]any) {
	t.Helper()
	if u["ssn"] != "[REDACTED]" {
		t.Errorf("ssn = %v", u["ssn"])
	}
	if got, _ := u["email"].(string); got == "ada@example.com" || !strings.HasSuffix(got, ".com") {
		t.Errorf("email not masked: %v", got)
	}
	if got, _ := u["phone"].(string); !strings.HasPrefix(got, "sha256:") {
		t.Errorf("phone not hashed: %v", got)
	}
	if addr, _ := u["address"].(map[string]any); addr["street"] != "[REDACTED]" {
		t.Errorf("street not redacted: %v", addr)
	}
	if _, present := u["Token"]; present {
		t.Error("Token must be omitted")
	}
	if u["name"] != "Ada Lovelace" {
		t.Errorf("name altered: %v", u["name"])
	}
	if u["age"] != float64(36) {
		t.Errorf("age altered: %v", u["age"])
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAutoRedaction(t *testing.T) {
	assertRedacted(t, logToMap(t, Config{Redact: true, HashSalt: "s"}, slog.Any("user", sampleUser())))
}

func TestSafeWithoutAutoRedaction(t *testing.T) {
	SetHashSalt("s")
	assertRedacted(t, logToMap(t, Config{Redact: false}, slog.Any("user", Safe(sampleUser()))))
}

func TestNonPIITypeUntouched(t *testing.T) {
	type plain struct {
		A string `json:"a"`
		B int    `json:"b"`
	}
	var buf bytes.Buffer
	lg, err := New(Config{Output: &buf, Redact: true})
	if err != nil {
		t.Fatal(err)
	}
	lg.LogAttrs(context.Background(), slog.LevelInfo, "t", slog.Any("p", plain{A: "x", B: 1}))
	var rec map[string]any
	_ = json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec)
	p := rec["p"].(map[string]any)
	if p["a"] != "x" || p["b"] != float64(1) {
		t.Errorf("plain struct altered: %v", p)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":      slog.LevelInfo,
		"debug": slog.LevelDebug,
		"INFO":  slog.LevelInfo,
		"Warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for in, want := range cases {
		got, err := parseLevel(in)
		if err != nil || got != want {
			t.Errorf("parseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseLevel("nope"); err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestNewRejectsBadFormat(t *testing.T) {
	if _, err := New(Config{Format: "xml"}); err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestInitSetsDefault(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Init(Config{Output: &buf, Format: FormatText}); err != nil {
		t.Fatal(err)
	}
	slog.Info("hi")
	if !strings.Contains(buf.String(), "hi") {
		t.Errorf("default logger not installed: %q", buf.String())
	}
}

func TestWithSinkFansOut(t *testing.T) {
	cap := newCapture()
	var buf bytes.Buffer
	lg, err := New(Config{Output: &buf}, WithSink(cap))
	if err != nil {
		t.Fatal(err)
	}
	lg.LogAttrs(context.Background(), slog.LevelInfo, "m", slog.String("k", "v"))
	if _, ok := cap.attrs["k"]; !ok {
		t.Error("extra sink did not receive record")
	}
	if !strings.Contains(buf.String(), "\"k\":\"v\"") {
		t.Errorf("local sink missing record: %q", buf.String())
	}
}

func TestConfigTags(t *testing.T) {
	rt := reflect.TypeOf(Config{})
	want := map[string][2]string{ // field -> {mapstructure, default}
		"Level":  {"level", "info"},
		"Format": {"format", "json"},
		"Redact": {"redact", "true"},
	}
	for f, exp := range want {
		sf, _ := rt.FieldByName(f)
		if sf.Tag.Get("mapstructure") != exp[0] {
			t.Errorf("%s mapstructure tag = %q, want %q", f, sf.Tag.Get("mapstructure"), exp[0])
		}
		if sf.Tag.Get("default") != exp[1] {
			t.Errorf("%s default tag = %q, want %q", f, sf.Tag.Get("default"), exp[1])
		}
	}
	if sf, _ := rt.FieldByName("HashSalt"); sf.Tag.Get("sensitive") != "true" {
		t.Errorf("HashSalt should be sensitive:\"true\", got %q", sf.Tag.Get("sensitive"))
	}
}
