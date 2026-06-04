package bootstrap

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func hasCmd(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func TestServeWiresDaemonCommands(t *testing.T) {
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp()
	err := Serve(root, app, func(context.Context, *Runtime) error { return nil },
		WithConfigSource(fakeSource{}), WithAppName("t"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"service", "__monitor", "__worker"} {
		if !hasCmd(root, name) {
			t.Errorf("Serve did not attach %q command", name)
		}
	}
}

func TestServeDirectRunCallsCore(t *testing.T) {
	called := false
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp() // Features.Daemon false
	if err := Serve(root, app, func(ctx context.Context, rt *Runtime) error {
		called = true
		if rt.Logger == nil {
			t.Error("nil logger in core")
		}
		return nil
	}, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatal(err)
	}
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("core not called on direct (non-daemon) run")
	}
}

func TestServeWorkerCommandRunsCore(t *testing.T) {
	called := false
	root := &cobra.Command{Use: "t"}
	app := defaultTestApp()
	if err := Serve(root, app, func(ctx context.Context, rt *Runtime) error {
		called = true
		return nil
	}, WithConfigSource(fakeSource{}), WithAppName("t")); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"__worker"}) // worker role runs the core body in-process
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("core not called via __worker command")
	}
}

func TestDaemonFlagOverlay(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--daemon").PersistentFlags(), &b)
	if !b.Features.Daemon {
		t.Error("--daemon should set Features.Daemon")
	}
}
