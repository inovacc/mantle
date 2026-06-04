package bootstrap

import (
	"context"

	"github.com/inovacc/daemon/pkg/daemon"
	"github.com/spf13/cobra"
)

// Serve wires the runtime (like Configure) AND daemon supervision onto root, using
// core as both the direct run body and the daemon worker body.
//
// Without --daemon (Features.Daemon false), the root command runs core directly.
// With --daemon, the root runs core under the daemon supervisor (monitor spawns a
// worker that runs core). The public `service` and hidden `__monitor`/`__worker`
// commands are attached for supervised execution and start/stop/status.
//
// Daemon lifecycle logs use slog.Default(), which Configure installs as Mantle's
// redacting logger — so they are automatically redacted and trace-correlated.
func Serve[T Configurable](root *cobra.Command, app T, core func(context.Context, *Runtime) error, opts ...Option) error {
	if err := Configure(root, app, opts...); err != nil {
		return err
	}
	o := defaultOptions(root)
	for _, fn := range opts {
		fn(&o)
	}
	dopts := daemon.Options{
		BinaryName: o.appName,
		Version:    o.version,
		Serve: func(ctx context.Context, _ daemon.Ports) error {
			return core(ctx, FromContext(ctx))
		},
	}
	if err := daemon.AttachCommands(root, dopts); err != nil {
		return err
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if app.base().Features.Daemon {
			return daemon.RunMonitor(cmd.Context(), dopts)
		}
		return core(cmd.Context(), FromContext(cmd.Context()))
	}
	return nil
}
