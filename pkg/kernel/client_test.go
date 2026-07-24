package kernel_test

import (
	"testing"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	logv1 "github.com/pluggableharness/agent/pkg/log/proto/v1"
)

func TestClient_LoadTelemetryConfig(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{
		getTelemetryConfigFunc: func(*kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error) {
			return &kernelv1.GetTelemetryConfigResult{
				TracesEnabled:  true,
				MetricsEnabled: false,
				LogsEnabled:    true,
				LogLevel:       logv1.LogLevel_LOG_LEVEL_DEBUG,
				SamplingRatio:  0.5,
			}, nil
		},
	}
	c := newTestClient(t, srv)

	if err := c.LoadTelemetryConfig(t.Context()); err != nil {
		t.Fatalf("LoadTelemetryConfig: %v", err)
	}
	if !c.TracingEnabled() {
		t.Error("TracingEnabled() = false, want true")
	}
	if c.MetricsEnabled() {
		t.Error("MetricsEnabled() = true, want false")
	}
	if !c.LogsEnabled() {
		t.Error("LogsEnabled() = false, want true")
	}
	if c.LogLevel() != logv1.LogLevel_LOG_LEVEL_DEBUG {
		t.Errorf("LogLevel() = %v, want LOG_LEVEL_DEBUG", c.LogLevel())
	}
	if c.SamplingRatio() != 0.5 {
		t.Errorf("SamplingRatio() = %v, want 0.5", c.SamplingRatio())
	}
}

func TestClient_defaultsBeforeLoad(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{})

	if c.TracingEnabled() {
		t.Error("TracingEnabled() before LoadTelemetryConfig = true, want false (conservative default)")
	}
	if c.LogLevel() != logv1.LogLevel_LOG_LEVEL_INFO {
		t.Errorf("LogLevel() before LoadTelemetryConfig = %v, want LOG_LEVEL_INFO", c.LogLevel())
	}
	if c.SamplingRatio() != 1.0 {
		t.Errorf("SamplingRatio() before LoadTelemetryConfig = %v, want 1.0", c.SamplingRatio())
	}
}

func TestClient_Raw(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{})
	if c.Raw() == nil {
		t.Fatal("Raw() = nil")
	}
}

func TestClient_Close(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{})
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
