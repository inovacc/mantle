package logger

import (
	"log/slog"
	"reflect"
	"sync/atomic"
)

// defaultRedactor backs the package-level Safe helper. It is stored atomically
// so SetHashSalt can be called concurrently with Safe without a data race.
var defaultRedactor atomic.Pointer[Redactor]

func init() { defaultRedactor.Store(NewRedactor("")) }

// SetHashSalt sets the salt used by Safe's "hash" strategy. Rotate it per
// deployment so hashed identifiers cannot be trivially reversed. It is safe to
// call concurrently with logging, and does not affect loggers created by New
// (configure Config.HashSalt for those).
func SetHashSalt(salt string) { defaultRedactor.Store(NewRedactor(salt)) }

// Safe wraps any value so that, when logged, PII-tagged fields are redacted.
// Because it implements slog.LogValuer, redaction is deferred until the record
// is handled — costing nothing on lines dropped by level.
//
//	logger.Info("signup", slog.Any("user", logger.Safe(u)))
func Safe(v any) slog.LogValuer { return safeValue{v: v, r: defaultRedactor.Load()} }

type safeValue struct {
	v any
	r *Redactor
}

func (s safeValue) LogValue() slog.Value { return s.r.Value(reflect.ValueOf(s.v), 0) }
