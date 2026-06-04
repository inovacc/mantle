package bootstrap

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Configure wires the always-present flags and a PersistentPreRunE onto root that
// loads config, overlays flags, builds the Runtime, and stores it in the command
// context. app must squash-embed Base (Configurable). T is typically *App.
func Configure[T Configurable](root *cobra.Command, app T, opts ...Option) error {
	o := defaultOptions(root)
	for _, fn := range opts {
		fn(&o)
	}
	if o.version != "" {
		root.Version = o.version
	}
	registerFlags(root)

	prev := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if prev != nil {
			if err := prev(cmd, args); err != nil {
				return err
			}
		}
		path := o.configPath
		if cp, _ := cmd.Flags().GetString("config"); cp != "" {
			path = cp
		}
		if err := o.source.Load(any(app), path, o.envPrefix); err != nil {
			return fmt.Errorf("bootstrap: load config: %w", err)
		}
		b := app.base()
		overlay(cmd.Flags(), b)
		rt, err := buildRuntime(cmd.Context(), b, o, any(app))
		if err != nil {
			return err
		}
		cmd.SetContext(withRuntime(cmd.Context(), rt))
		return nil
	}
	return nil
}

func defaultOptions(root *cobra.Command) options {
	return options{
		source:     viperSource{},
		configPath: "config.yaml",
		version:    "dev",
		appName:    root.Name(),
	}
}
