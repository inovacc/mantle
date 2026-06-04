// Package bootstrap wires a Cobra CLI to a core application: it loads unified
// config (defaults -> file -> env), overlays always-present flags, gates optional
// subsystems (logging, observability) behind feature flags, and hands the core
// app a Runtime (logger, tracer, meter, shutdown). The flow is cobra -> wrapper
// -> core app.
//
//	type App struct {
//	    bootstrap.Base `mapstructure:",squash" yaml:",inline"`
//	    Greeting string `mapstructure:"greeting"`
//	}
//	app := &App{Base: bootstrap.DefaultBase()}
//	root := &cobra.Command{Use: "myapp", RunE: func(cmd *cobra.Command, _ []string) error {
//	    return bootstrap.Run(cmd, func(ctx context.Context, rt *bootstrap.Runtime) error {
//	        rt.Logger.InfoContext(ctx, "hello")
//	        return rt.Shutdown(ctx)
//	    })
//	}}
//	bootstrap.Configure(root, app, bootstrap.WithAppName("myapp"))
//	root.Execute()
//
// Defaults are applied programmatically via DefaultBase (the loader ignores
// `default:` struct tags). CLI flags outrank file and env. Daemon mode arrives
// in a later milestone.
package bootstrap
