package obsv

import (
	"fmt"
	"time"
)

// Protocol values for OTLP export.
const (
	ProtocolGRPC = "grpc"
	ProtocolHTTP = "http"
)

// Config controls observability bootstrap. Tags let the inovacc/config loader
// populate it. The zero value is disabled; set Enabled to turn it on.
type Config struct {
	Enabled        bool              `mapstructure:"enabled"         yaml:"enabled"`
	Endpoint       string            `mapstructure:"endpoint"        yaml:"endpoint"`         // OTLP host:port; "" => stdout (dev)
	Protocol       string            `mapstructure:"protocol"        yaml:"protocol"         default:"grpc"`
	Insecure       bool              `mapstructure:"insecure"        yaml:"insecure"`         // skip TLS
	Headers        map[string]string `mapstructure:"headers"         yaml:"headers"`          // OTLP auth headers
	Signals        Signals           `mapstructure:"signals"         yaml:"signals"`
	SampleRatio    float64           `mapstructure:"sample_ratio"    yaml:"sample_ratio"     default:"1.0"`
	MetricInterval time.Duration     `mapstructure:"metric_interval" yaml:"metric_interval"  default:"15s"`
	// RuntimeMetrics enables Go runtime metrics. Defaults true when loaded via
	// config (default tag); programmatic callers must set it explicitly.
	RuntimeMetrics bool `mapstructure:"runtime_metrics" yaml:"runtime_metrics" default:"true"`
}

// Signals toggles individual pipelines. The zero value (all false) means
// "all on" when Config.Enabled; set one or more true to restrict.
type Signals struct {
	Logs    bool `mapstructure:"logs"    yaml:"logs"`
	Traces  bool `mapstructure:"traces"  yaml:"traces"`
	Metrics bool `mapstructure:"metrics" yaml:"metrics"`
}

// ServiceInfo identifies the service for the OTel resource.
type ServiceInfo struct{ Name, Version, Environment string }

// normalize applies defaults and the all-off-means-all-on Signals rule, and
// validates Protocol. It does not toggle RuntimeMetrics (bool zero is ambiguous).
func (c *Config) normalize() error {
	if c.Protocol == "" {
		c.Protocol = ProtocolGRPC
	}
	if c.Protocol != ProtocolGRPC && c.Protocol != ProtocolHTTP {
		return fmt.Errorf("obsv: invalid protocol %q (want grpc or http)", c.Protocol)
	}
	if c.SampleRatio == 0 {
		c.SampleRatio = 1.0
	}
	if c.MetricInterval <= 0 {
		c.MetricInterval = 15 * time.Second
	}
	if !c.Signals.Logs && !c.Signals.Traces && !c.Signals.Metrics {
		c.Signals = Signals{Logs: true, Traces: true, Metrics: true}
	}
	return nil
}
