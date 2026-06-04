# pkg/logger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the seed `piilog` package into a pure, importable `github.com/inovacc/mantle/logger` — slog + tag-driven PII redaction + trace correlation + `Safe()` — with OTel export removed (it moves to `pkg/obsv`) and a `WithSink` seam for it to plug back in.

**Architecture:** Handler chain `redactHandler → traceHandler → fanoutHandler → [JSON/Text sink, …extra sinks]`. Redaction is outermost so no sink ever sees raw PII. The redaction engine is a single recursive walker returning `slog.Value` (struct→`GroupValue`; slice/map→`AnyValue` of `[]any`/`map[string]any` built by converting child values), backed by one per-type cache holding both type-scan flags and the field plan.

**Tech Stack:** Go 1.25, `log/slog`, `reflect`, `go.opentelemetry.io/otel/trace` (API only — no SDK). BSD-3-Clause. Table-driven tests, ≥80% coverage, `-race` clean.

---

## File Structure

```
go.mod                       module github.com/inovacc/mantle, go 1.25
LICENSE                      BSD-3-Clause (holder: dyammarcano, 2026)
.gitignore                   Go + .scripts/ + coverage artifacts
pkg/logger/
  doc.go                     package doc + tag reference
  logger.go                  Config, Format, options, New, Init, parseLevel
  handler.go                 redactHandler, traceHandler, fanoutHandler
  redact.go                  Redactor, typeInfo cache, plan, strategies, walker, valueToAny
  safe.go                    Safe, safeValue, SetHashSalt, defaultRedactor
  logger_test.go             Config/level/New/Init + full-pipeline (auto-redact, Safe, sink, trace)
  handler_test.go            per-handler unit tests + redact-before-fanout invariant
  redact_test.go             Redactor.Value, mask, hash, omit, typeFlags, Safe (engine-level)
  bench_test.go              redaction + pipeline benchmarks
```

**Removed:** root seed files `logger.go`, `handler.go`, `redact.go`, `otel.go`, `main.go`, `redact_test.go` (superseded; `otel.go` content moves to `pkg/obsv` in sub-project B, `main.go` → `cmd/logger` in sub-project B). `README.md` is rewritten in Task 5.

**Note on commits:** this directory is not yet a git repo; Task 1 initializes one so per-task commits work.

---

### Task 1: Module scaffold

**Files:**
- Create: `go.mod`, `LICENSE`, `.gitignore`, `pkg/logger/doc.go`
- Delete: `logger.go`, `handler.go`, `redact.go`, `otel.go`, `main.go`, `redact_test.go` (repo root)

- [ ] **Step 1: Initialize git and remove seed root sources**

Run:
```bash
cd D:/weaver-sync/modules/logger
git init
rm -f logger.go handler.go redact.go otel.go main.go redact_test.go
```
Expected: `Initialized empty Git repository`; the six root `.go` files are gone (the captured source lives in this plan).

- [ ] **Step 2: Create `go.mod`**

`go.mod`:
```
module github.com/inovacc/mantle

go 1.25

require go.opentelemetry.io/otel/trace v1.32.0
```

- [ ] **Step 3: Create `LICENSE`** (BSD-3-Clause, matching the fleet)

`LICENSE`:
```
BSD 3-Clause License

Copyright (c) 2026, dyammarcano

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

- [ ] **Step 4: Create `.gitignore`**

`.gitignore`:
```
# Binaries / build
*.exe
*.out
/dist/
# Scripts (ephemeral per project policy)
.scripts/
# Coverage
coverage*.out
coverage*.html
# Local memory
.remember/
```

- [ ] **Step 5: Create `pkg/logger/doc.go`**

`pkg/logger/doc.go`:
```go
// Package logger provides an opinionated structured-logging package built on the
// standard library's log/slog.
//
// It gives client code three things:
//
//   - A configured *slog.Logger writing JSON or text to a local sink, with extra
//     fan-out sinks pluggable via WithSink (e.g. an OpenTelemetry bridge from
//     github.com/inovacc/mantle/obsv).
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
```

- [ ] **Step 6: Verify it builds and commit**

Run:
```bash
go build ./...
git add -A && git commit -m "chore(logger): scaffold module, remove seed root sources"
```
Expected: `go build` exits 0 (the package compiles with only a doc file); commit succeeds.

---

### Task 2: Redaction engine (`redact.go` + `safe.go`)

**Files:**
- Create: `pkg/logger/redact.go`, `pkg/logger/safe.go`
- Test: `pkg/logger/redact_test.go`

- [ ] **Step 1: Write the failing engine tests**

`pkg/logger/redact_test.go`:
```go
package logger

import (
	"reflect"
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
	if a.hash("x") != a.hash("x") {
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
	if !infoFor(reflect.TypeOf(user{})).hasPII {
		t.Error("user{} should report hasPII")
	}
	type plain struct {
		A string `json:"a"`
	}
	if infoFor(reflect.TypeOf(plain{})).hasPII {
		t.Error("plain{} should not report hasPII")
	}
	type box struct {
		V any `json:"v"`
	}
	if !infoFor(reflect.TypeOf(box{})).hasIface {
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
	for i := 0; i < defaultMaxDepth+3; i++ {
		head = &node{Secret: "s", Next: head}
	}
	v := NewRedactor("s").Value(reflect.ValueOf(*head), 0)
	if v.Kind().String() == "" {
		t.Fatal("nil value")
	}
	// Should not panic / infinite-loop; deep nodes collapse to a marker.
	if !strings.Contains(valueToString(t, v), "max-depth-exceeded") {
		t.Error("expected max-depth marker in deep graph")
	}
}

func valueToString(t *testing.T, v interface{ Any() any }) string {
	t.Helper()
	return strings.ToLower(strings.TrimSpace(toStr(v.Any())))
}
func toStr(a any) string {
	switch x := a.(type) {
	case map[string]any:
		var b strings.Builder
		for k, val := range x {
			b.WriteString(k + ":" + toStr(val) + " ")
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, val := range x {
			b.WriteString(toStr(val) + " ")
		}
		return b.String()
	default:
		return strings.TrimSpace(strings_Sprint(a))
	}
}
func strings_Sprint(a any) string { return reflectSprint(a) }
```

> Note: `TestMaxDepth`'s string-walk helpers are deliberately dependency-free. They are replaced by a simpler assertion once `valueToAny` exists — keep them minimal. If `reflectSprint` is awkward, assert by walking the `map[string]any` for the marker instead. (Implementation step below provides `valueToAny`; the test only needs it.)

Simplify the max-depth helper to avoid the `reflectSprint` placeholder — replace the bottom of the file (`valueToString` through `strings_Sprint`) with:
```go
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
		for _, v := range x {
			if containsMarker(v) {
				return true
			}
		}
	}
	return false
}
```
and change `TestMaxDepth`'s final assertion to:
```go
	if !containsMarker(valueToAny(v)) {
		t.Error("expected max-depth marker in deep graph")
	}
```
(Delete `valueToString`, `toStr`, `strings_Sprint`, `strings_Sprint`/`reflectSprint`.)

- [ ] **Step 2: Run tests to verify they fail (do not compile)**

Run: `go test ./pkg/logger/ -run TestRedactor -v`
Expected: FAIL — `undefined: NewRedactor`, `valueToAny`, `mask`, `infoFor`, etc.

- [ ] **Step 3: Implement `pkg/logger/redact.go`**

`pkg/logger/redact.go`:
```go
package logger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tagKey          = "pii"
	placeholder     = "[REDACTED]"
	defaultMaxDepth = 8
)

var timeType = reflect.TypeOf(time.Time{})

type strategy int

const (
	stratRedact strategy = iota
	stratMask
	stratHash
	stratOmit
)

type tagOpts struct{ keep int }

// Redactor turns arbitrary Go values into a redacted, log-safe representation.
// Per-type decisions are memoised so the hot path is indexed field access plus
// the chosen action. A Redactor is safe for concurrent use.
type Redactor struct {
	salt     string
	maxDepth int
}

// NewRedactor returns a Redactor that salts the "hash" strategy with salt.
func NewRedactor(salt string) *Redactor {
	return &Redactor{salt: salt, maxDepth: defaultMaxDepth}
}

// ---------------------------------------------------------------------------
// Single per-type cache: type-scan flags + (lazy) field plan.
// ---------------------------------------------------------------------------

type emitKind int

const (
	emitStrategy emitKind = iota
	emitRecurse
	emitScalar
	emitPassthrough
)

type fieldPlan struct {
	index    int
	key      string
	emit     emitKind
	strategy strategy
	keep     int
}

type structPlan struct{ fields []fieldPlan }

type typeInfo struct {
	hasPII   bool
	hasIface bool
	once     sync.Once
	planv    *structPlan
}

func (ti *typeInfo) plan(t reflect.Type) *structPlan {
	ti.once.Do(func() { ti.planv = buildPlan(t) })
	return ti.planv
}

var infoCache sync.Map // reflect.Type -> *typeInfo

func infoFor(t reflect.Type) *typeInfo {
	if v, ok := infoCache.Load(t); ok {
		return v.(*typeInfo)
	}
	pii, iface := scanType(t, map[reflect.Type]bool{})
	ti := &typeInfo{hasPII: pii, hasIface: iface}
	actual, _ := infoCache.LoadOrStore(t, ti)
	return actual.(*typeInfo)
}

// scanType reports, in one pass, whether t (or anything it transitively holds)
// carries a pii tag and whether its static shape includes an interface kind.
func scanType(t reflect.Type, seen map[reflect.Type]bool) (hasPII, hasIface bool) {
	if t == nil || seen[t] {
		return false, false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Interface:
		return false, true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return scanType(t.Elem(), seen)
	case reflect.Map:
		kp, ki := scanType(t.Key(), seen)
		vp, vi := scanType(t.Elem(), seen)
		return kp || vp, ki || vi
	case reflect.Struct:
		if t == timeType {
			return false, false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if _, ok := f.Tag.Lookup(tagKey); ok {
				hasPII = true
			}
			fp, fi := scanType(f.Type, seen)
			hasPII = hasPII || fp
			hasIface = hasIface || fi
		}
		return hasPII, hasIface
	}
	return false, false
}

func buildPlan(t reflect.Type) *structPlan {
	fields := make([]fieldPlan, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, jsonDash := fieldName(f)
		if raw, ok := f.Tag.Lookup(tagKey); ok {
			strat, opts := parseTag(raw)
			if strat == stratOmit {
				continue
			}
			fields = append(fields, fieldPlan{index: i, key: name, emit: emitStrategy, strategy: strat, keep: opts.keep})
			continue
		}
		if jsonDash {
			continue
		}
		bt := baseType(f.Type)
		info := infoFor(f.Type)
		switch {
		case bt.Kind() == reflect.Interface || info.hasIface:
			fields = append(fields, fieldPlan{index: i, key: name, emit: emitRecurse})
		case info.hasPII:
			fields = append(fields, fieldPlan{index: i, key: name, emit: emitRecurse})
		case isScalarKind(bt.Kind()):
			fields = append(fields, fieldPlan{index: i, key: name, emit: emitScalar})
		default:
			fields = append(fields, fieldPlan{index: i, key: name, emit: emitPassthrough})
		}
	}
	return &structPlan{fields: fields}
}

// ---------------------------------------------------------------------------
// Single walker: returns slog.Value for all kinds. Containers are emitted as
// AnyValue([]any / map[string]any), with elements converted via valueToAny so
// JSON/Text handlers render arrays/objects correctly.
// ---------------------------------------------------------------------------

// Value redacts v and returns its log-safe slog.Value. depth should be 0 at the
// top level; it bounds recursion against cyclic/over-deep graphs.
func (r *Redactor) Value(v reflect.Value, depth int) slog.Value {
	if depth > r.maxDepth {
		return slog.StringValue("[max-depth-exceeded]")
	}
	v = deref(v)
	if !v.IsValid() {
		return slog.AnyValue(nil)
	}
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == timeType {
			return slog.TimeValue(v.Interface().(time.Time))
		}
		return r.structGroup(v, depth)
	case reflect.Slice, reflect.Array:
		return slog.AnyValue(r.list(v, depth))
	case reflect.Map:
		return slog.AnyValue(r.mapp(v, depth))
	default:
		return scalarValue(v)
	}
}

func (r *Redactor) structGroup(v reflect.Value, depth int) slog.Value {
	plan := infoFor(v.Type()).plan(v.Type())
	attrs := make([]slog.Attr, 0, len(plan.fields))
	for i := range plan.fields {
		fp := &plan.fields[i]
		fv := v.Field(fp.index)
		var val slog.Value
		switch fp.emit {
		case emitStrategy:
			val = slog.StringValue(r.strategyString(fp, fv))
		case emitRecurse:
			val = r.Value(fv, depth+1)
		case emitScalar:
			val = scalarValue(fv)
		default:
			val = passthrough(fv)
		}
		attrs = append(attrs, slog.Attr{Key: fp.key, Value: val})
	}
	return slog.GroupValue(attrs...)
}

func (r *Redactor) list(v reflect.Value, depth int) []any {
	if v.Kind() == reflect.Slice && v.IsNil() {
		return nil
	}
	n := v.Len()
	out := make([]any, n)
	for i := 0; i < n; i++ {
		out[i] = valueToAny(r.Value(v.Index(i), depth+1))
	}
	return out
}

func (r *Redactor) mapp(v reflect.Value, depth int) map[string]any {
	if v.IsNil() {
		return nil
	}
	out := make(map[string]any, v.Len())
	it := v.MapRange()
	for it.Next() {
		out[fmt.Sprintf("%v", it.Key().Interface())] = valueToAny(r.Value(it.Value(), depth+1))
	}
	return out
}

// valueToAny converts a (possibly group) slog.Value into a plain Go value so it
// can live inside []any / map[string]any container representations.
func valueToAny(v slog.Value) any {
	if v.Kind() == slog.KindGroup {
		g := v.Group()
		m := make(map[string]any, len(g))
		for _, a := range g {
			m[a.Key] = valueToAny(a.Value)
		}
		return m
	}
	return v.Any()
}

// ---------------------------------------------------------------------------
// strategies
// ---------------------------------------------------------------------------

func (r *Redactor) strategyString(fp *fieldPlan, v reflect.Value) string {
	switch fp.strategy {
	case stratMask:
		return mask(stringify(v), fp.keep)
	case stratHash:
		return r.hash(stringify(v))
	default:
		return placeholder
	}
}

func (r *Redactor) hash(s string) string {
	sum := sha256.Sum256([]byte(r.salt + s))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

func mask(s string, keep int) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}
	if keep <= 0 || keep >= n {
		return strings.Repeat("*", n)
	}
	return strings.Repeat("*", n-keep) + string(runes[n-keep:])
}

func stringify(v reflect.Value) string {
	v = deref(v)
	if !v.IsValid() {
		return ""
	}
	if v.CanInterface() {
		if s, ok := v.Interface().(fmt.Stringer); ok {
			return s.String()
		}
		return fmt.Sprintf("%v", v.Interface())
	}
	return fmt.Sprintf("%v", v)
}

// ---------------------------------------------------------------------------
// leaf helpers
// ---------------------------------------------------------------------------

func scalarValue(v reflect.Value) slog.Value {
	v = deref(v)
	if !v.IsValid() {
		return slog.AnyValue(nil)
	}
	switch v.Kind() {
	case reflect.String:
		return slog.StringValue(v.String())
	case reflect.Bool:
		return slog.BoolValue(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return slog.Int64Value(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return slog.Uint64Value(int64FromUint(v.Uint()))
	case reflect.Float32, reflect.Float64:
		return slog.Float64Value(v.Float())
	default:
		if v.CanInterface() {
			return slog.AnyValue(v.Interface())
		}
		return slog.StringValue(fmt.Sprintf("%v", v))
	}
}

// int64FromUint keeps slog.Uint64Value's argument type explicit (it takes uint64).
func int64FromUint(u uint64) uint64 { return u }

func passthrough(v reflect.Value) slog.Value {
	if !v.IsValid() {
		return slog.AnyValue(nil)
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return slog.AnyValue(nil)
	}
	if v.CanInterface() {
		return slog.AnyValue(v.Interface())
	}
	return slog.StringValue(fmt.Sprintf("%v", v))
}

// ---------------------------------------------------------------------------
// tag / type utilities
// ---------------------------------------------------------------------------

func parseTag(raw string) (strategy, tagOpts) {
	parts := strings.Split(raw, ",")
	opts := tagOpts{keep: 4}
	var s strategy
	switch strings.TrimSpace(parts[0]) {
	case "", "true", "redact", "pii":
		s = stratRedact
	case "mask":
		s = stratMask
	case "hash":
		s = stratHash
	case "-", "omit", "drop":
		s = stratOmit
	default:
		s = stratRedact
	}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case strings.HasPrefix(p, "keep="):
			if n, err := strconv.Atoi(p[len("keep="):]); err == nil {
				opts.keep = n
			}
		default:
			if n, err := strconv.Atoi(p); err == nil {
				opts.keep = n
			}
		}
	}
	return s, opts
}

func fieldName(f reflect.StructField) (name string, jsonDash bool) {
	if tag, ok := f.Tag.Lookup("json"); ok {
		if tag == "-" {
			return f.Name, true
		}
		if n := strings.Split(tag, ",")[0]; n != "" {
			return n, false
		}
	}
	return f.Name, false
}

func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func baseType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
```

> The `int64FromUint` helper is only there to make the `Uint64Value(uint64)` call read clearly; if your linter objects, inline `slog.Uint64Value(v.Uint())` directly (`v.Uint()` already returns `uint64`) and delete the helper.

- [ ] **Step 4: Implement `pkg/logger/safe.go`**

`pkg/logger/safe.go`:
```go
package logger

import (
	"log/slog"
	"reflect"
)

// defaultRedactor backs the package-level Safe helper. Configure its salt with
// SetHashSalt before logging if you rely on the "hash" strategy outside New.
var defaultRedactor = NewRedactor("")

// SetHashSalt sets the salt used by Safe's "hash" strategy. Rotate it per
// deployment so hashed identifiers cannot be trivially reversed. It does not
// affect loggers created by New (configure Config.HashSalt for those).
func SetHashSalt(salt string) { defaultRedactor = NewRedactor(salt) }

// Safe wraps any value so that, when logged, PII-tagged fields are redacted.
// Because it implements slog.LogValuer, redaction is deferred until the record
// is handled — costing nothing on lines dropped by level.
//
//	logger.Info("signup", slog.Any("user", logger.Safe(u)))
func Safe(v any) slog.LogValuer { return safeValue{v: v, r: defaultRedactor} }

type safeValue struct {
	v any
	r *Redactor
}

func (s safeValue) LogValue() slog.Value { return s.r.Value(reflect.ValueOf(s.v), 0) }
```

- [ ] **Step 5: Add the engine-level Safe test**

Append to `pkg/logger/redact_test.go`:
```go
func TestSafeRedactsViaLogValue(t *testing.T) {
	SetHashSalt("s")
	v := Safe(sampleUser()).LogValue()
	m := valueToAny(v).(map[string]any)
	if m["ssn"] != "[REDACTED]" {
		t.Errorf("Safe did not redact ssn: %v", m["ssn"])
	}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./pkg/logger/ -run 'TestRedactor|TestMask|TestHash|TestTypeFlags|TestMaxDepth|TestSafe' -v`
Expected: PASS (all engine tests green).

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(logger): redaction engine with single walker and unified type cache"
```

---

### Task 3: Handlers (`handler.go`)

**Files:**
- Create: `pkg/logger/handler.go`
- Test: `pkg/logger/handler_test.go`

- [ ] **Step 1: Write the failing handler tests**

`pkg/logger/handler_test.go`:
```go
package logger

import (
	"context"
	"errors"
	"log/slog"
	"testing"

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
```

Add the tiny time helper at the bottom of `handler_test.go` (tests must not call the banned argless `new(Date)` — use a fixed time):
```go
import "time"

func time_Now() time.Time { return time.Unix(1700000000, 0).UTC() }
```
(Place the `import "time"` in the existing import block rather than a second block.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/logger/ -run 'Handler|Fanout|Trace' -v`
Expected: FAIL — `undefined: redactHandler`, `traceHandler`, `fanoutHandler`.

- [ ] **Step 3: Implement `pkg/logger/handler.go`**

`pkg/logger/handler.go`:
```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/logger/ -run 'Handler|Fanout|Trace' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(logger): redact/trace/fanout slog handlers"
```

---

### Task 4: Assembly — Config, options, New, Init (`logger.go`)

**Files:**
- Create: `pkg/logger/logger.go`
- Test: `pkg/logger/logger_test.go`

- [ ] **Step 1: Write the failing assembly + pipeline tests**

`pkg/logger/logger_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/logger/ -run 'Auto|Safe|NonPII|ParseLevel|NewRejects|Init|WithSink|ConfigTags' -v`
Expected: FAIL — `undefined: New`, `Init`, `Config`, `parseLevel`, `FormatJSON`, `WithSink`.

- [ ] **Step 3: Implement `pkg/logger/logger.go`**

`pkg/logger/logger.go`:
```go
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format selects the local output encoding.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// Config controls logger construction. Its struct tags let the inovacc/config
// loader populate it directly (viper/mapstructure, yaml, env) with no coupling.
// The zero value is usable: JSON at info level to stdout with redaction on.
type Config struct {
	ServiceName string `mapstructure:"service_name" yaml:"service_name" env:"SERVICE_NAME"`
	Level       string `mapstructure:"level"        yaml:"level"         env:"LEVEL"       default:"info"`
	Format      Format `mapstructure:"format"       yaml:"format"        env:"FORMAT"      default:"json"`
	AddSource   bool   `mapstructure:"add_source"   yaml:"add_source"    env:"ADD_SOURCE"`
	Redact      bool   `mapstructure:"redact"       yaml:"redact"        env:"REDACT"      default:"true"`
	HashSalt    string `mapstructure:"hash_salt"    yaml:"hash_salt"     env:"HASH_SALT"   sensitive:"true"`

	// Output is the local sink. Defaults to os.Stdout. Not loaded from config.
	Output io.Writer `mapstructure:"-" yaml:"-"`
}

type options struct {
	sinks       []slog.Handler
	replaceAttr func(groups []string, a slog.Attr) slog.Attr
	redactor    *Redactor
}

// Option customizes logger construction.
type Option func(*options)

// WithSink adds an extra slog.Handler that receives every (redacted, trace-
// correlated) record alongside the local sink — e.g. pkg/obsv's OTel bridge.
func WithSink(h slog.Handler) Option {
	return func(o *options) {
		if h != nil {
			o.sinks = append(o.sinks, h)
		}
	}
}

// WithReplaceAttr sets slog.HandlerOptions.ReplaceAttr on the local sink.
func WithReplaceAttr(fn func(groups []string, a slog.Attr) slog.Attr) Option {
	return func(o *options) { o.replaceAttr = fn }
}

// WithRedactor overrides the Redactor used when Config.Redact is true.
func WithRedactor(r *Redactor) Option {
	return func(o *options) { o.redactor = r }
}

// parseLevel maps "debug"/"info"/"warn"/"error" (case-insensitive, "" = info)
// to a slog.Level. It also accepts slog's offset syntax (e.g. "INFO+2").
func parseLevel(s string) (slog.Level, error) {
	if strings.TrimSpace(s) == "" {
		return slog.LevelInfo, nil
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("logger: invalid level %q: %w", s, err)
	}
	return l, nil
}

// New constructs a configured *slog.Logger. It is pure: no telemetry export and
// nothing to shut down (OTel export lives in pkg/obsv and attaches via WithSink).
func New(cfg Config, opts ...Option) (*slog.Logger, error) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}

	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}
	switch cfg.Format {
	case "":
		cfg.Format = FormatJSON
	case FormatJSON, FormatText:
	default:
		return nil, fmt.Errorf("logger: invalid format %q", cfg.Format)
	}
	lvl, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	hopts := &slog.HandlerOptions{Level: lvl, AddSource: cfg.AddSource, ReplaceAttr: o.replaceAttr}
	var base slog.Handler
	if cfg.Format == FormatText {
		base = slog.NewTextHandler(cfg.Output, hopts)
	} else {
		base = slog.NewJSONHandler(cfg.Output, hopts)
	}

	// Compose: fanout (if extra sinks) -> trace enrichment -> redaction (outer).
	sinks := append([]slog.Handler{base}, o.sinks...)
	var h slog.Handler = base
	if len(sinks) > 1 {
		h = fanoutHandler{handlers: sinks}
	}
	h = traceHandler{next: h}
	if cfg.Redact {
		rd := o.redactor
		if rd == nil {
			rd = NewRedactor(cfg.HashSalt)
		}
		h = redactHandler{next: h, r: rd}
	}

	lg := slog.New(h)
	if cfg.ServiceName != "" {
		lg = lg.With(slog.String("service", cfg.ServiceName))
	}
	return lg, nil
}

// Init builds a logger with New, installs it as the slog default, and syncs the
// package Safe salt to Config.HashSalt.
func Init(cfg Config, opts ...Option) (*slog.Logger, error) {
	lg, err := New(cfg, opts...)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(lg)
	if cfg.HashSalt != "" {
		SetHashSalt(cfg.HashSalt)
	}
	return lg, nil
}
```

- [ ] **Step 4: Run the full package test suite**

Run: `go test ./pkg/logger/ -v`
Expected: PASS (all tasks 2–4 tests green together).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(logger): Config, options, New/Init assembly with WithSink seam"
```

---

### Task 5: Benchmarks, README, verification

**Files:**
- Create: `pkg/logger/bench_test.go`
- Rewrite: `README.md`

- [ ] **Step 1: Create `pkg/logger/bench_test.go`**

`pkg/logger/bench_test.go`:
```go
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lg.LogAttrs(ctx, slog.LevelInfo, "evt", slog.Any("p", p))
	}
}

// Below-threshold lines must be nearly free with Safe (deferred LogValuer).
func BenchmarkLoggerSafeFilteredOut(b *testing.B) {
	lg, _ := New(Config{Output: io.Discard, Level: "info"})
	ctx := context.Background()
	u := sampleUser()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lg.LogAttrs(ctx, slog.LevelDebug, "trace", slog.Any("user", Safe(u)))
	}
}
```

- [ ] **Step 2: Run benchmarks (smoke, 1x) to confirm they build and run**

Run: `go test ./pkg/logger/ -run x -bench . -benchtime 1x`
Expected: each `Benchmark*` prints a line; no failures.

- [ ] **Step 3: Rewrite `README.md`**

`README.md`:
```markdown
# logger

`github.com/inovacc/mantle` — structured logging on `log/slog` with tag-driven
PII redaction and OpenTelemetry trace correlation. Part of the inovacc fleet
(`config`, `daemon`, `logger`).

## Install

```bash
go get github.com/inovacc/mantle/logger
```

## Quick start

```go
import "github.com/inovacc/mantle/logger"

lg, err := logger.New(logger.Config{
    ServiceName: "checkout-api",
    Level:       "info",   // debug | info | warn | error
    Format:      "json",   // json | text
    Redact:      true,     // tag-driven PII redaction (on by default)
    HashSalt:    "rotate-me",
})
if err != nil { panic(err) }

lg.InfoContext(ctx, "user signed up", slog.Any("user", u))
```

## PII redaction

Tag struct fields with `pii:"..."`:

| Tag | Effect |
|-----|--------|
| `pii:"redact"` / `pii:"true"` | replace with `[REDACTED]` |
| `pii:"mask"` / `pii:"mask,2"` / `pii:"mask,keep=6"` | reveal last N chars (default 4) |
| `pii:"hash"` | salted SHA-256 digest (`sha256:` + 16 hex) |
| `pii:"-"` / `omit` / `drop` | drop the field |
| `json:"-"` | also dropped from logs |

Opt in per value without global redaction:

```go
lg.Info("signup", slog.Any("user", logger.Safe(u)))
```

## Observability

`pkg/logger` depends only on the stdlib and the OTel **trace API** (for
`trace_id`/`span_id` correlation). Full OpenTelemetry export (logs, traces,
metrics) is provided by `pkg/obsv`, which attaches via:

```go
lg, _ := logger.New(cfg, logger.WithSink(otelBridgeHandler))
```

## License

BSD-3-Clause.
```

- [ ] **Step 4: Tidy, vet, race, coverage**

Run:
```bash
go mod tidy
go vet ./...
go test ./pkg/logger/ -race -coverprofile="$TEMP/logger_cover.out"
go tool cover -func="$TEMP/logger_cover.out" | tail -1
```
Expected: `go mod tidy` resolves `go.opentelemetry.io/otel` (provides `/trace`) + `stretchr/testify` is NOT added (we use stdlib testing); `go vet` clean; `-race` PASS; total coverage ≥ 80%.

- [ ] **Step 5: Confirm the dependency boundary holds**

Run: `go list -deps ./pkg/logger | grep opentelemetry`
Expected: only `go.opentelemetry.io/otel/trace` and its minimal internal deps — **no** `go.opentelemetry.io/otel/sdk`, no exporters, no bridges.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "test(logger): benchmarks; docs(logger): rewrite README; chore: tidy + verify deps"
```

---

## Self-Review

**1. Spec coverage** (against `2026-06-04-pkg-logger-design.md`):
- §3 deps (stdlib + otel/trace only) → Task 5 Step 5 verifies the boundary. ✓
- §4 public API (Format, Config, Option, WithSink/WithReplaceAttr/WithRedactor, New, Init, Safe, SetHashSalt, Redactor) → Tasks 2–4 implement all; `New` drops ctx/ShutdownFunc; `Level` is string; `Redact` default true. ✓
- §5 handler chain + invariants → Task 3 (`TestRedactHandlerScrubsBeforeNext`, fanout, trace). ✓
- §6 refactored engine (single walker, single cache, strategies, json:"-", maxDepth, Safe shares redactor) → Task 2. ✓
- §7 error handling (invalid level/format, fanout errors.Join) → `TestNewRejectsBadFormat`, `TestParseLevel`, `TestFanoutJoinsErrors`. ✓
- §8 tests (table-driven, ≥80%) → Tasks 2–5. ✓
- §9 file layout → matches; `otel.go`/`main.go` removed (move to sub-project B). ✓
- §10 acceptance #4 (Config tag round-trip) → implemented as `TestConfigTags` via reflection (keeps deps pure — no mapstructure test dep). **Deviation noted & justified.** #5 (untagged passthrough) → `BenchmarkLoggerPlainStructRedactOn`. ✓

**2. Placeholder scan:** The only soft spot was `TestMaxDepth`'s string helper; Step 1 of Task 2 replaces it with the concrete `containsMarker` helper. No TBD/TODO remain.

**3. Type consistency:** `Redactor` (pointer `*Redactor`) used consistently in `redactHandler.r`, `safeValue.r`, `WithRedactor`, `NewRedactor`. Walker entry is `Value` (capital) everywhere. `infoFor(...).hasPII` used in both `buildPlan` and `redactHandler.redactAttr`. `valueToAny` defined in `redact.go`, used in tests and `list`/`mapp`. `FormatJSON`/`FormatText` consistent. `parseLevel` signature `(slog.Level, error)` matches caller in `New`.

**Known carried-forward gaps (out of scope for A, by spec §2):** top-level interface-only structs aren't auto-redacted unless wrapped with `Safe` (preserved seed behavior); no regex/content PII detection; OTel batch tuning lives in `pkg/obsv`.
