package bootstrap

import (
	"github.com/inovacc/logger/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// registerFlags adds the always-present persistent flags to root.
func registerFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	pf.StringP("config", "c", "", "config file path")
	pf.String("env", "", "environment (dev|staging|prod)")
	pf.String("log-level", "", "log level (debug|info|warn|error)")
	pf.BoolP("verbose", "v", false, "verbose logging (debug level)")
	pf.BoolP("quiet", "q", false, "quiet logging (error level)")
	pf.String("log-format", "", "log format (json|text)")
	pf.Bool("log-source", false, "include source file:line in logs")
	pf.Bool("no-redact", false, "disable PII redaction")
	pf.Bool("otel", false, "enable OpenTelemetry observability")
	pf.String("otel-endpoint", "", "OTLP endpoint host:port")
	pf.String("otel-protocol", "", "OTLP protocol (grpc|http)")
}

// overlay applies CHANGED flags onto b (highest precedence). pflag.FlagSet.Visit
// visits only flags the user actually set.
func overlay(fs *pflag.FlagSet, b *Base) {
	fs.Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "env":
			b.Environment = f.Value.String()
		case "log-level":
			b.Logger.Level = f.Value.String()
		case "verbose":
			b.Logger.Level = "debug"
		case "quiet":
			b.Logger.Level = "error"
		case "log-format":
			b.Logger.Format = logger.Format(f.Value.String())
		case "log-source":
			b.Logger.AddSource = true
		case "no-redact":
			b.Logger.Redact = false
		case "otel":
			b.Features.Observability = true
			b.Observability.Enabled = true
		case "otel-endpoint":
			b.Observability.Endpoint = f.Value.String()
		case "otel-protocol":
			b.Observability.Protocol = f.Value.String()
		}
	})
}
