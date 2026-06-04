package bootstrap

import (
	"time"

	"github.com/inovacc/mantle/logger"
	"github.com/inovacc/mantle/obsv"
)

// Features gates optional subsystems.
type Features struct {
	Logging       bool `mapstructure:"logging"       yaml:"logging"`
	Observability bool `mapstructure:"observability" yaml:"observability"`
	Daemon        bool `mapstructure:"daemon"        yaml:"daemon"`
}

// Base is the wrapper-provided config block. A user app squash-embeds it:
//
//	type App struct {
//	    bootstrap.Base `mapstructure:",squash" yaml:",inline"`
//	    MyField string `mapstructure:"my_field"`
//	}
type Base struct {
	Environment   string        `mapstructure:"environment"   yaml:"environment"`
	Features      Features      `mapstructure:"features"      yaml:"features"`
	Logger        logger.Config `mapstructure:"logger"        yaml:"logger"`
	Observability obsv.Config   `mapstructure:"observability" yaml:"observability"`
}

func (b *Base) base() *Base { return b }

// Configurable is satisfied only by structs that squash-embed Base (the base()
// method is unexported, so it cannot be implemented outside this package).
type Configurable interface{ base() *Base }

// DefaultBase returns programmatic defaults. The underlying config loader ignores
// `default:` struct tags, so callers seed their app with this before Configure.
func DefaultBase() Base {
	return Base{
		Environment: "dev",
		Features:    Features{Logging: true},
		Logger: logger.Config{
			Level:  "info",
			Format: logger.FormatJSON,
			Redact: true,
		},
		Observability: obsv.Config{
			Protocol:       obsv.ProtocolGRPC,
			SampleRatio:    1.0,
			MetricInterval: 15 * time.Second,
			RuntimeMetrics: true,
		},
	}
}
