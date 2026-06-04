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

var timeType = reflect.TypeFor[time.Time]()

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
		for i := range t.NumField() {
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
	for i := range t.NumField() {
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
	for i := range n {
		out[i] = valueToAny(r.Value(v.Index(i), depth+1))
	}
	return out
}

func (r *Redactor) mapp(v reflect.Value, depth int) map[string]any {
	if v.IsNil() {
		return nil
	}
	out := make(map[string]any, v.Len())
	// note: keys are rendered with %v; string-renderable keys are the intended case. Distinct keys that render identically would collide.
	it := v.MapRange()
	for it.Next() {
		out[fmt.Sprintf("%v", it.Key().Interface())] = valueToAny(r.Value(it.Value(), depth+1))
	}
	return out
}

// known gap: a slog.LogValuer stored inside a container (slice/map/any field) is not resolved or redacted here; only top-level LogValuers are resolved by slog. Wrap nested values with Safe explicitly if needed.

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

// hash returns a salted SHA-256 fingerprint truncated to 64 bits: a stable correlation token, not a cryptographic commitment. Low-entropy inputs remain brute-forceable.
func (r *Redactor) hash(s string) string {
	sum := sha256.Sum256([]byte(r.salt + s))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// mask reveals the last keep runes and stars the rest. Note: the number of stars reveals the original length.
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
		return slog.Uint64Value(v.Uint())
	case reflect.Float32, reflect.Float64:
		return slog.Float64Value(v.Float())
	default:
		if v.CanInterface() {
			return slog.AnyValue(v.Interface())
		}
		return slog.StringValue(fmt.Sprintf("%v", v))
	}
}

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
