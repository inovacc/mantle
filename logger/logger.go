package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format selects the log encoding for the local sink.
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

// options carries functional-option state for New.
type options struct {
	sinks       []slog.Handler
	replaceAttr func(groups []string, a slog.Attr) slog.Attr
	redactor    *Redactor
}

// Option is a functional option for New.
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

// New builds and returns a configured *slog.Logger. It is pure: no global state
// is modified. Use Init to also install the logger as the slog default.
func New(cfg Config, opts ...Option) (*slog.Logger, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
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
