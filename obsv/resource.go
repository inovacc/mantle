package obsv

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func buildResource(ctx context.Context, info ServiceInfo) (*resource.Resource, error) {
	attrs := make([]attribute.KeyValue, 0, 3)
	if info.Name != "" {
		attrs = append(attrs, semconv.ServiceName(info.Name))
	}

	if info.Version != "" {
		attrs = append(attrs, semconv.ServiceVersion(info.Version))
	}

	if info.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment", info.Environment))
	}

	return resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithProcess(),
		resource.WithHost(),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
}
