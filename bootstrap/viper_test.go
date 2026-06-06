package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestViperSourceLoadsYAML is the single integration test that exercises the real
// viper-backed ConfigSource (and the loader's process-global state). Self-contained.
func TestViperSourceLoadsYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := "service:\n  logger:\n    level: warn\n  greeting: from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	app := defaultTestApp() // level info, greeting hi

	root := &cobra.Command{Use: "t", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	if err := Configure(root, app, WithAppName("t"), WithConfigPath(path)); err != nil {
		t.Fatal(err)
	}

	root.SetArgs(nil)

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if app.Logger.Level != "warn" {
		t.Errorf("file did not override level: %q", app.Logger.Level)
	}

	if app.Greeting != "from-file" {
		t.Errorf("file did not set greeting: %q", app.Greeting)
	}
}
