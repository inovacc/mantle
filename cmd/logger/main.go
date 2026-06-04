package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/inovacc/mantle/bootstrap"
	"github.com/spf13/cobra"
)

// App composes the wrapper's Base with the app's own fields.
type App struct {
	bootstrap.Base `mapstructure:",squash" yaml:",inline"`
	Greeting       string `mapstructure:"greeting" yaml:"greeting"`
}

func main() {
	app := &App{Base: bootstrap.DefaultBase(), Greeting: "hello"}

	root := &cobra.Command{
		Use:   "logger-demo",
		Short: "Demonstrates cobra -> bootstrap wrapper -> core app (with optional daemon mode)",
	}

	core := func(ctx context.Context, rt *bootstrap.Runtime) error {
		a := bootstrap.ConfigOf[*App](rt)
		rt.Logger.InfoContext(ctx, "core app running",
			slog.String("greeting", a.Greeting),
			slog.String("env", a.Environment),
		)
		return rt.Shutdown(ctx)
	}

	if err := bootstrap.Serve(root, app, core,
		bootstrap.WithAppName("logger-demo"),
		bootstrap.WithVersion("0.1.0"),
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
