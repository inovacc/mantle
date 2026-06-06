package bootstrap

import (
	"testing"

	"github.com/spf13/cobra"
)

func parsedFlags(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	root := &cobra.Command{Use: "t"}
	registerFlags(root)

	if err := root.PersistentFlags().Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}

	return root
}

func TestOverlayLevel(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--log-level", "warn").PersistentFlags(), &b)

	if b.Logger.Level != "warn" {
		t.Errorf("level = %q, want warn", b.Logger.Level)
	}
}

func TestOverlayVerboseQuiet(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "-v").PersistentFlags(), &b)

	if b.Logger.Level != "debug" {
		t.Errorf("verbose → %q, want debug", b.Logger.Level)
	}

	b = DefaultBase()
	overlay(parsedFlags(t, "-q").PersistentFlags(), &b)

	if b.Logger.Level != "error" {
		t.Errorf("quiet → %q, want error", b.Logger.Level)
	}
}

func TestOverlayNoRedactAndOtel(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--no-redact", "--otel", "--otel-endpoint", "h:4317", "--otel-protocol", "http").PersistentFlags(), &b)

	if b.Logger.Redact {
		t.Error("--no-redact should disable redaction")
	}

	if !b.Features.Observability || !b.Observability.Enabled {
		t.Error("--otel should enable observability")
	}

	if b.Observability.Endpoint != "h:4317" || b.Observability.Protocol != "http" {
		t.Errorf("otel endpoint/protocol not overlaid: %+v", b.Observability)
	}
}

func TestOverlayNoFlagsKeepsDefaults(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t).PersistentFlags(), &b)

	if b.Logger.Level != "info" || !b.Logger.Redact {
		t.Errorf("defaults changed without flags: %+v", b.Logger)
	}
}

func TestOverlayFormatSourceEnv(t *testing.T) {
	b := DefaultBase()
	overlay(parsedFlags(t, "--log-format", "text", "--log-source", "--env", "prod").PersistentFlags(), &b)

	if b.Logger.Format != "text" {
		t.Errorf("format = %q, want text", b.Logger.Format)
	}

	if !b.Logger.AddSource {
		t.Error("--log-source should set AddSource")
	}

	if b.Environment != "prod" {
		t.Errorf("env = %q, want prod", b.Environment)
	}
}
