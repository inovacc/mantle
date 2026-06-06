package obsv

import (
	"testing"
	"time"
)

func TestNormalizeDefaults(t *testing.T) {
	c := Config{Enabled: true}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}

	if c.Protocol != ProtocolGRPC {
		t.Errorf("protocol = %q, want grpc", c.Protocol)
	}

	if c.SampleRatio != 1.0 {
		t.Errorf("sample = %v, want 1.0", c.SampleRatio)
	}

	if c.MetricInterval != 15*time.Second {
		t.Errorf("interval = %v, want 15s", c.MetricInterval)
	}

	if !c.Signals.Logs || !c.Signals.Traces || !c.Signals.Metrics {
		t.Errorf("all-off signals should normalize to all-on: %+v", c.Signals)
	}
}

func TestNormalizeLogsOnly(t *testing.T) {
	c := Config{Enabled: true, Signals: Signals{Logs: true}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}

	if !c.Signals.Logs || c.Signals.Traces || c.Signals.Metrics {
		t.Errorf("expected logs-only, got %+v", c.Signals)
	}
}

func TestNormalizeInvalidProtocol(t *testing.T) {
	c := Config{Enabled: true, Protocol: "xml"}
	if err := c.normalize(); err == nil {
		t.Error("expected error for invalid protocol")
	}
}
