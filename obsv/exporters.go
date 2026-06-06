package obsv

import (
	"context"
	"io"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func buildTraceExporter(ctx context.Context, cfg Config, w io.Writer) (sdktrace.SpanExporter, error) {
	if cfg.Endpoint == "" {
		return stdouttrace.New(stdouttrace.WithWriter(w))
	}

	if cfg.Protocol == ProtocolHTTP {
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}

		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}

		return otlptracehttp.New(ctx, opts...)
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
	}

	return otlptracegrpc.New(ctx, opts...)
}

func buildMetricExporter(ctx context.Context, cfg Config, w io.Writer) (sdkmetric.Exporter, error) {
	if cfg.Endpoint == "" {
		return stdoutmetric.New(stdoutmetric.WithWriter(w))
	}

	if cfg.Protocol == ProtocolHTTP {
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}

		if len(cfg.Headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
		}

		return otlpmetrichttp.New(ctx, opts...)
	}

	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}

	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
	}

	return otlpmetricgrpc.New(ctx, opts...)
}

func buildLogExporter(ctx context.Context, cfg Config, w io.Writer) (sdklog.Exporter, error) {
	if cfg.Endpoint == "" {
		return stdoutlog.New(stdoutlog.WithWriter(w))
	}

	if cfg.Protocol == ProtocolHTTP {
		opts := []otlploghttp.Option{otlploghttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}

		if len(cfg.Headers) > 0 {
			opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
		}

		return otlploghttp.New(ctx, opts...)
	}

	opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}

	if len(cfg.Headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(cfg.Headers))
	}

	return otlploggrpc.New(ctx, opts...)
}
