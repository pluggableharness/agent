package config

import (
	"context"
	"reflect"
	"testing"
)

func TestDecodeSettings_observabilityFull(t *testing.T) {
	t.Parallel()

	path := writeHCL(t, `
required_providers {
  anthropic = { source = "github.com/agentco/provider-anthropic", version = "~> 1.0" }
}

settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true

  observability {
    endpoint            = "otel-collector:4317"
    protocol            = "grpc"
    sampling_ratio      = 0.5
    traces_enabled      = true
    metrics_enabled     = false
    logs_enabled        = true
    export_interval_ms  = 5000
    service_name        = "pluggableharness-agent-kernel"
    resource_attrs      = { env = "prod", region = "us-east" }
  }
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	want := Observability{
		Endpoint:         "otel-collector:4317",
		Protocol:         "grpc",
		SamplingRatio:    0.5,
		TracesEnabled:    true,
		MetricsEnabled:   false,
		LogsEnabled:      true,
		ExportIntervalMS: 5000,
		ServiceName:      "pluggableharness-agent-kernel",
		ResourceAttrs:    map[string]string{"env": "prod", "region": "us-east"},
	}
	if !reflect.DeepEqual(cfg.Settings.Observability, want) {
		t.Fatalf("Settings.Observability = %+v, want %+v", cfg.Settings.Observability, want)
	}
}

func TestDecodeSettings_observabilityResourceAttrsOptional(t *testing.T) {
	t.Parallel()

	path := writeHCL(t, `
required_providers {
  anthropic = { source = "github.com/agentco/provider-anthropic", version = "~> 1.0" }
}

settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true

  observability {
    endpoint            = "localhost:4317"
    protocol            = "http"
    sampling_ratio      = 1.0
    traces_enabled      = true
    metrics_enabled     = true
    logs_enabled        = true
    export_interval_ms  = 10000
    service_name        = "pluggableharness-agent"
  }
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Settings.Observability.ResourceAttrs != nil {
		t.Errorf("ResourceAttrs = %v, want nil when omitted", cfg.Settings.Observability.ResourceAttrs)
	}
	if cfg.Settings.Observability.Protocol != "http" {
		t.Errorf("Protocol = %q, want http", cfg.Settings.Observability.Protocol)
	}
}
