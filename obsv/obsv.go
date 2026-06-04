package obsv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	otellogglobal "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type options struct{ stdout io.Writer }

// Option customizes New.
type Option func(*options)

// WithStdoutWriter routes the dev stdout exporters (used when Config.Endpoint is
// empty) to w. No effect when an OTLP Endpoint is set. Also the test seam.
func WithStdoutWriter(w io.Writer) Option {
	return func(o *options) {
		if w != nil {
			o.stdout = w
		}
	}
}

type shutdownFunc func(context.Context) error

// Stack holds the bootstrapped OTel providers and a unified shutdown.
type Stack struct {
	scope    string
	tracerP  trace.TracerProvider  // nil => no-op
	meterP   metric.MeterProvider  // nil => no-op
	logSink  slog.Handler          // nil => logs disabled
	shutdown []shutdownFunc
	once     sync.Once
	shutErr  error
}

// New bootstraps observability per cfg. When cfg.Enabled is false it returns a
// fully no-op Stack (never nil, no globals touched).
func New(ctx context.Context, cfg Config, info ServiceInfo, opts ...Option) (*Stack, error) {
	if !cfg.Enabled {
		return noopStack(info), nil
	}
	o := options{stdout: os.Stdout}
	for _, fn := range opts {
		fn(&o)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	res, err := buildResource(ctx, info)
	if err != nil && res == nil {
		// resource.New returns nil only on a fatal error; a non-fatal partial
		// error (e.g. schema-URL conflict or a single detector failing) comes
		// back with a usable res, which we keep.
		return nil, fmt.Errorf("obsv: resource: %w", err)
	}

	st := &Stack{scope: scopeName(info)}
	// On a partial-build failure, fail shuts down already-built providers, but a
	// global already set via otel.SetTracerProvider/SetMeterProvider may still
	// point at the (now shut-down) provider. That's acceptable: New returns an
	// error, the caller must not use telemetry, and the propagator is not set on
	// the failure path.
	fail := func(e error) (*Stack, error) {
		_ = st.Shutdown(context.Background())
		return nil, e
	}

	if cfg.Signals.Traces {
		exp, err := buildTraceExporter(ctx, cfg, o.stdout)
		if err != nil {
			return fail(fmt.Errorf("obsv: trace exporter: %w", err))
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exp),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
		)
		otel.SetTracerProvider(tp)
		st.tracerP = tp
		st.shutdown = append(st.shutdown, tp.Shutdown)
	}

	if cfg.Signals.Metrics {
		exp, err := buildMetricExporter(ctx, cfg, o.stdout)
		if err != nil {
			return fail(fmt.Errorf("obsv: metric exporter: %w", err))
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(cfg.MetricInterval))),
		)
		otel.SetMeterProvider(mp)
		st.meterP = mp
		st.shutdown = append(st.shutdown, mp.Shutdown)
		if cfg.RuntimeMetrics {
			if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
				return fail(fmt.Errorf("obsv: runtime metrics: %w", err))
			}
		}
	}

	if cfg.Signals.Logs {
		exp, err := buildLogExporter(ctx, cfg, o.stdout)
		if err != nil {
			return fail(fmt.Errorf("obsv: log exporter: %w", err))
		}
		lp := sdklog.NewLoggerProvider(
			sdklog.WithResource(res),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		)
		otellogglobal.SetLoggerProvider(lp)
		st.logSink = otelslog.NewHandler(st.scope, otelslog.WithLoggerProvider(lp))
		st.shutdown = append(st.shutdown, lp.Shutdown)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return st, nil
}

// Tracer returns a tracer (no-op when traces are disabled).
func (s *Stack) Tracer(name string) trace.Tracer {
	if s.tracerP == nil {
		return tracenoop.NewTracerProvider().Tracer(name)
	}
	return s.tracerP.Tracer(name)
}

// Meter returns a meter (no-op when metrics are disabled).
func (s *Stack) Meter(name string) metric.Meter {
	if s.meterP == nil {
		return metricnoop.NewMeterProvider().Meter(name)
	}
	return s.meterP.Meter(name)
}

// LogSink returns the slog.Handler bridging to the OTel LoggerProvider, or nil
// when logs are disabled. Pass it to logger.WithSink (nil is a safe no-op there).
func (s *Stack) LogSink() slog.Handler { return s.logSink }

// Shutdown flushes and shuts down all enabled providers (in reverse build order).
// Safe to call multiple times and on a no-op Stack.
func (s *Stack) Shutdown(ctx context.Context) error {
	s.once.Do(func() {
		var errs error
		for i := len(s.shutdown) - 1; i >= 0; i-- {
			errs = errors.Join(errs, s.shutdown[i](ctx))
		}
		s.shutErr = errs
	})
	return s.shutErr
}
