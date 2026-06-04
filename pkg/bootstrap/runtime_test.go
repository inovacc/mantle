package bootstrap

import (
	"context"
	"io"
	"testing"

	"github.com/inovacc/mantle/pkg/obsv"
	"github.com/spf13/cobra"
)

type fakeSource struct{}

// Load is a no-op: the app keeps its caller-seeded values (no global state).
func (fakeSource) Load(app any, path, envPrefix string) error { return nil }

func defaultTestApp() *testApp {
	a := &testApp{Base: DefaultBase(), Greeting: "hi"}
	a.Logger.Output = io.Discard // keep test stdout clean
	return a
}

func runWith(t *testing.T, app *testApp) *Runtime {
	t.Helper()
	var captured *Runtime
	root := &cobra.Command{Use: "t"}
	sub := &cobra.Command{Use: "go", RunE: func(cmd *cobra.Command, args []string) error {
		captured = FromContext(cmd.Context())
		return nil
	}}
	root.AddCommand(sub)
	if err := Configure(root, app, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	root.SetArgs([]string{"go"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured == nil {
		t.Fatal("runtime not captured")
	}
	return captured
}

func TestConfigureBuildsRuntime(t *testing.T) {
	app := defaultTestApp()
	app.Features.Observability = true
	app.Observability.RuntimeMetrics = false       // no background goroutine in tests
	app.Observability.Signals = obsv.Signals{Traces: true} // minimal, quiet
	rt := runWith(t, app)
	if rt.Logger == nil || rt.Tracer == nil || rt.Meter == nil || rt.Shutdown == nil {
		t.Fatal("runtime fields must be non-nil")
	}
	if got := ConfigOf[*testApp](rt); got == nil || got.Greeting != "hi" {
		t.Errorf("ConfigOf returned %v", got)
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestFeatureGatingLoggingOnly(t *testing.T) {
	app := defaultTestApp() // Logging on, Observability off
	rt := runWith(t, app)
	if rt.Logger == nil || rt.Tracer == nil || rt.Meter == nil {
		t.Fatal("accessors must be non-nil even with observability off")
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown should be nil with observability off: %v", err)
	}
}

func TestFromContextNoRuntime(t *testing.T) {
	rt := FromContext(context.Background())
	if rt == nil || rt.Logger == nil || rt.Tracer == nil || rt.Meter == nil || rt.Shutdown == nil {
		t.Fatal("FromContext must return a safe no-op runtime (never nil)")
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown: %v", err)
	}
}

func TestRunHandoff(t *testing.T) {
	called := false
	root := &cobra.Command{Use: "t"}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return Run(cmd, func(ctx context.Context, rt *Runtime) error {
			called = true
			if rt.Logger == nil {
				t.Error("rt.Logger nil in core handler")
			}
			return nil
		})
	}
	app := defaultTestApp()
	if err := Configure(root, app, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatal(err)
	}
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("core func was not called")
	}
}

func TestOptionsVersionApplied(t *testing.T) {
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp()
	if err := Configure(root, app, WithConfigSource(fakeSource{}), WithEnvPrefix("APP"), WithVersion("9.9.9")); err != nil {
		t.Fatal(err)
	}
	if root.Version != "9.9.9" {
		t.Errorf("root.Version = %q, want 9.9.9", root.Version)
	}
}

func TestObservabilityProducesRecordingSpan(t *testing.T) {
	app := defaultTestApp()
	app.Features.Observability = true
	app.Observability.RuntimeMetrics = false
	app.Observability.Signals = obsv.Signals{Traces: true}
	rt := runWith(t, app)
	_, span := rt.Tracer.Start(context.Background(), "op")
	if !span.IsRecording() {
		t.Error("real tracer should produce a recording span")
	}
	span.End()
	_ = rt.Shutdown(context.Background())
}

func TestNoObservabilityNoRecordingSpan(t *testing.T) {
	app := defaultTestApp() // observability off → no-op tracer
	rt := runWith(t, app)
	_, span := rt.Tracer.Start(context.Background(), "op")
	if span.IsRecording() {
		t.Error("no-op tracer span should not be recording")
	}
	span.End()
}
