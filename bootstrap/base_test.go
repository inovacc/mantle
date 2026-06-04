package bootstrap

import (
	"testing"

	"github.com/inovacc/mantle/logger"
)

// testApp is the in-package fixture: a user app squash-embedding Base.
type testApp struct {
	Base     `mapstructure:",squash" yaml:",inline"`
	Greeting string `mapstructure:"greeting" yaml:"greeting"`
}

func TestDefaultBase(t *testing.T) {
	b := DefaultBase()
	if !b.Features.Logging {
		t.Error("Logging should default on")
	}
	if !b.Logger.Redact {
		t.Error("Redact should default true")
	}
	if b.Logger.Level != "info" {
		t.Errorf("level = %q, want info", b.Logger.Level)
	}
	if b.Logger.Format != logger.FormatJSON {
		t.Errorf("format = %q, want json", b.Logger.Format)
	}
	if !b.Observability.RuntimeMetrics {
		t.Error("RuntimeMetrics should default true")
	}
}

func TestConfigurableSatisfiedByEmbedding(t *testing.T) {
	var c Configurable = &testApp{Base: DefaultBase()}
	if c.base() == nil {
		t.Error("base() should return the embedded Base")
	}
}
