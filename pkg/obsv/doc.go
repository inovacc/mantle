// Package obsv bootstraps OpenTelemetry for a service: logs, traces, and metrics
// providers with OTLP (gRPC/HTTP) or stdout exporters, a detected resource, W3C
// propagation, and Go runtime metrics.
//
// One call returns a Stack:
//
//	stack, err := obsv.New(ctx, cfg, obsv.ServiceInfo{Name: "checkout", Version: "1.2.0"})
//	defer stack.Shutdown(ctx)
//	lg, _ := logger.New(logCfg, logger.WithSink(stack.LogSink()))
//	tracer := stack.Tracer("checkout")
//	meter := stack.Meter("checkout")
//
// When Config.Enabled is false, New returns a fully no-op Stack (no-op tracer and
// meter, a nil LogSink, and a no-op Shutdown) so callers need no conditionals.
//
// This package owns the OpenTelemetry SDK dependencies; the sibling pkg/logger
// stays dependency-light (it imports only the OTel trace API).
package obsv
