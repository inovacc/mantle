package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/inovacc/mantle/logger"
	"github.com/inovacc/mantle/obsv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Runtime is what the wrapper hands the core app.
type Runtime struct {
	Cfg      any                         // the user's *App
	Logger   *slog.Logger                // always non-nil
	Tracer   trace.Tracer                // no-op when observability is off
	Meter    metric.Meter                // no-op when observability is off
	Shutdown func(context.Context) error // flushes observability; always non-nil
}

type runtimeKey struct{}

func withRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// FromContext returns the Runtime stored by Configure, or a safe no-op Runtime
// (never nil) if none is present.
func FromContext(ctx context.Context) *Runtime {
	if rt, ok := ctx.Value(runtimeKey{}).(*Runtime); ok && rt != nil {
		return rt
	}
	return noopRuntime()
}

func noopRuntime() *Runtime {
	return &Runtime{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:   tracenoop.NewTracerProvider().Tracer("noop"),
		Meter:    metricnoop.NewMeterProvider().Meter("noop"),
		Shutdown: func(context.Context) error { return nil },
	}
}

// ConfigOf returns the typed config from the Runtime (the *App passed to Configure).
func ConfigOf[T any](rt *Runtime) T {
	v, _ := rt.Cfg.(T)
	return v
}

// Run retrieves the Runtime from the command context and invokes the core handler.
func Run(cmd *cobra.Command, core func(context.Context, *Runtime) error) error {
	ctx := cmd.Context()
	return core(ctx, FromContext(ctx))
}

// buildRuntime constructs observability first (so its LogSink can attach to the
// logger), then the logger, and a combined Shutdown.
func buildRuntime(ctx context.Context, b *Base, o options, cfg any) (*Runtime, error) {
	rt := &Runtime{Cfg: cfg, Shutdown: func(context.Context) error { return nil }}
	var sink slog.Handler
	var shutdowns []func(context.Context) error

	if b.Features.Observability {
		b.Observability.Enabled = true
		stack, err := obsv.New(ctx, b.Observability, obsv.ServiceInfo{
			Name:        o.appName,
			Version:     o.version,
			Environment: b.Environment,
		})
		if err != nil {
			return nil, fmt.Errorf("bootstrap: observability: %w", err)
		}
		sink = stack.LogSink()
		rt.Tracer = stack.Tracer(o.appName)
		rt.Meter = stack.Meter(o.appName)
		shutdowns = append(shutdowns, stack.Shutdown)
	} else {
		rt.Tracer = tracenoop.NewTracerProvider().Tracer(o.appName)
		rt.Meter = metricnoop.NewMeterProvider().Meter(o.appName)
	}

	if b.Features.Logging {
		lg, err := logger.Init(b.Logger, logger.WithSink(sink)) // WithSink(nil) is a no-op
		if err != nil {
			return nil, fmt.Errorf("bootstrap: logger: %w", err)
		}
		rt.Logger = lg
	} else {
		rt.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if len(shutdowns) > 0 {
		rt.Shutdown = func(c context.Context) error {
			var errs error
			for i := len(shutdowns) - 1; i >= 0; i-- {
				errs = errors.Join(errs, shutdowns[i](c))
			}
			return errs
		}
	}
	return rt, nil
}
