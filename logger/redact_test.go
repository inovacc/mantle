package logger

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type address struct {
	Street string `json:"street" pii:"redact"`
	City   string `json:"city"`
	Zip    string `json:"zip" pii:"mask,2"`
}

type user struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email" pii:"mask,4"`
	SSN       string    `json:"ssn" pii:"redact"`
	Phone     string    `json:"phone" pii:"hash"`
	Age       int       `json:"age"`
	Active    bool      `json:"active"`
	Token     string    `json:"-" pii:"-"`
	Addr      address   `json:"address"`
	CreatedAt time.Time `json:"created_at"`
}

func sampleUser() user {
	return user{
		ID: "u_123", Name: "Ada Lovelace",
		Email: "ada@example.com", SSN: "123-45-6789",
		Phone: "+1-555-0100", Age: 36, Active: true, Token: "secret",
		Addr:      address{Street: "5 Analytical Way", City: "London", Zip: "EC1A1BB"},
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	}
}

// groupMap converts a struct's redacted slog.Value (a group) into a map for asserts.
func groupMap(t *testing.T, v reflect.Value) map[string]any {
	t.Helper()

	out := NewRedactor("s").Value(v, 0)
	m := valueToAny(out)

	mm, ok := m.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", m)
	}

	return mm
}

func TestRedactorValueStruct(t *testing.T) {
	u := groupMap(t, reflect.ValueOf(sampleUser()))
	if u["ssn"] != "[REDACTED]" {
		t.Errorf("ssn = %v, want [REDACTED]", u["ssn"])
	}

	if got, _ := u["email"].(string); got == "ada@example.com" || !strings.HasSuffix(got, ".com") {
		t.Errorf("email not masked: %v", got)
	}

	if got, _ := u["phone"].(string); !strings.HasPrefix(got, "sha256:") {
		t.Errorf("phone not hashed: %v", got)
	}

	if addr, _ := u["address"].(map[string]any); addr["street"] != "[REDACTED]" {
		t.Errorf("address.street not redacted: %v", addr)
	}

	if _, present := u["Token"]; present {
		t.Error("Token must be omitted")
	}

	if u["name"] != "Ada Lovelace" {
		t.Errorf("non-PII name altered: %v", u["name"])
	}
}

func TestRedactorSliceOfPII(t *testing.T) {
	v := NewRedactor("s").Value(reflect.ValueOf([]user{sampleUser(), sampleUser()}), 0)

	list, ok := v.Any().([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2-element list, got %T", v.Any())
	}

	if first := list[0].(map[string]any); first["ssn"] != "[REDACTED]" {
		t.Errorf("slice element not redacted: %v", first["ssn"])
	}
}

func TestMaskKeep(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""}, {"ab", "**"}, {"abcd", "****"}, {"abcde", "*bcde"},
	}
	for _, c := range cases {
		if got := mask(c.in, 4); got != c.want {
			t.Errorf("mask(%q,4) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashStableAndSalted(t *testing.T) {
	a := NewRedactor("salt-a")
	b := NewRedactor("salt-b")

	h1 := a.hash("x")
	if h1 != a.hash("x") {
		t.Error("hash not stable for same salt")
	}

	if a.hash("x") == b.hash("x") {
		t.Error("hash should differ across salts")
	}

	if !strings.HasPrefix(a.hash("x"), "sha256:") || len(a.hash("x")) != len("sha256:")+16 {
		t.Errorf("unexpected hash shape: %q", a.hash("x"))
	}
}

func TestTypeFlags(t *testing.T) {
	if !infoFor(reflect.TypeFor[user]()).hasPII {
		t.Error("user{} should report hasPII")
	}

	type plain struct {
		A string `json:"a"`
	}

	if infoFor(reflect.TypeFor[plain]()).hasPII {
		t.Error("plain{} should not report hasPII")
	}

	type box struct {
		V any `json:"v"`
	}

	if !infoFor(reflect.TypeFor[box]()).hasIface {
		t.Error("box{} should report hasIface")
	}
}

func TestMaxDepth(t *testing.T) {
	type node struct {
		Secret string `json:"secret" pii:"redact"`
		Next   *node  `json:"next"`
	}
	// Build a chain deeper than defaultMaxDepth.
	var head *node
	for range defaultMaxDepth + 3 {
		head = &node{Secret: "s", Next: head}
	}

	v := NewRedactor("s").Value(reflect.ValueOf(*head), 0)
	if v.Kind().String() == "" {
		t.Fatal("nil value")
	}
	// Should not panic / infinite-loop; deep nodes collapse to a marker.
	if !containsMarker(valueToAny(v)) {
		t.Error("expected max-depth marker in deep graph")
	}
}

func containsMarker(a any) bool {
	switch x := a.(type) {
	case string:
		return strings.Contains(x, "max-depth-exceeded")
	case map[string]any:
		for _, v := range x {
			if containsMarker(v) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(x, containsMarker)
	}

	return false
}

func TestSafeRedactsViaLogValue(t *testing.T) {
	SetHashSalt("s")

	v := Safe(sampleUser()).LogValue()

	m := valueToAny(v).(map[string]any)
	if m["ssn"] != "[REDACTED]" {
		t.Errorf("Safe did not redact ssn: %v", m["ssn"])
	}
}

func TestRedactorMapOfPII(t *testing.T) {
	m := map[string]user{"a": sampleUser()}
	v := NewRedactor("s").Value(reflect.ValueOf(m), 0)

	out, ok := v.Any().(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", v.Any())
	}

	inner, ok := out["a"].(map[string]any)
	if !ok {
		t.Fatalf("expected inner map, got %T", out["a"])
	}

	if inner["ssn"] != "[REDACTED]" {
		t.Errorf("map value PII not redacted: %v", inner["ssn"])
	}
}

func TestRedactorPointerField(t *testing.T) {
	type box struct {
		Addr *address `json:"addr"`
	}

	v := NewRedactor("s").Value(reflect.ValueOf(box{Addr: &address{Street: "x", City: "c", Zip: "12345"}}), 0)
	m := valueToAny(v).(map[string]any)

	addr, ok := m["addr"].(map[string]any)
	if !ok {
		t.Fatalf("expected addr map, got %T", m["addr"])
	}

	if addr["street"] != "[REDACTED]" {
		t.Errorf("pointer field PII not redacted: %v", addr["street"])
	}

	v2 := NewRedactor("s").Value(reflect.ValueOf(box{Addr: nil}), 0)

	m2 := valueToAny(v2).(map[string]any)
	if m2["addr"] != nil {
		t.Errorf("nil pointer field should be nil, got %v", m2["addr"])
	}
}

func TestMaskHashNonStringField(t *testing.T) {
	type acct struct {
		PIN  int `json:"pin" pii:"mask,2"`
		Card int `json:"card" pii:"hash"`
	}

	m := valueToAny(NewRedactor("s").Value(reflect.ValueOf(acct{PIN: 123456, Card: 99}), 0)).(map[string]any)
	if pin, _ := m["pin"].(string); !strings.HasSuffix(pin, "56") || !strings.HasPrefix(pin, "*") {
		t.Errorf("int mask unexpected: %v", pin)
	}

	if card, _ := m["card"].(string); !strings.HasPrefix(card, "sha256:") {
		t.Errorf("int hash unexpected: %v", card)
	}
}
