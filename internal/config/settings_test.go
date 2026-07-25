package config

import (
	"context"
	"reflect"
	"testing"

	"github.com/pluggableharness/agent/internal/doomloop"
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

// settingsHCL wraps body in the three attributes settings{} requires, so a
// test case only has to spell out the optional field it actually exercises.
func settingsHCL(body string) string {
	return `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = false
` + body + `
}
`
}

// TestDecodeSettings_subBlockDefaultsWhenBlockPresent covers the second
// half of this package's two-place defaulting rule: a settings{} block that
// is present but declares none of the optional sub-blocks/attributes still
// gets every canonical default. (The no-settings{}-block-at-all half lives
// in TestLoadFile_settingsDefaultsWhenAbsent.)
func TestDecodeSettings_subBlockDefaultsWhenBlockPresent(t *testing.T) {
	t.Parallel()

	path := writeHCL(t, settingsHCL(""))
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if cfg.Settings.EventBus != DefaultEventBus {
		t.Errorf("EventBus = %+v, want DefaultEventBus %+v", cfg.Settings.EventBus, DefaultEventBus)
	}
	if cfg.Settings.DoomLoop != DefaultDoomLoopSettings {
		t.Errorf("DoomLoop = %+v, want DefaultDoomLoopSettings %+v", cfg.Settings.DoomLoop, DefaultDoomLoopSettings)
	}
	if cfg.Settings.DefaultHookTimeoutMS != DefaultHookTimeoutMS {
		t.Errorf("DefaultHookTimeoutMS = %d, want %d", cfg.Settings.DefaultHookTimeoutMS, DefaultHookTimeoutMS)
	}
	if cfg.Settings.DefaultToolTimeoutMS != DefaultToolTimeoutMS {
		t.Errorf("DefaultToolTimeoutMS = %d, want %d", cfg.Settings.DefaultToolTimeoutMS, DefaultToolTimeoutMS)
	}
	if cfg.Settings.MaxDepth != nil {
		t.Errorf("MaxDepth = %d, want nil", *cfg.Settings.MaxDepth)
	}
	if cfg.Settings.Retry != DefaultRetrySettings {
		t.Errorf("Retry = %+v, want DefaultRetrySettings %+v", cfg.Settings.Retry, DefaultRetrySettings)
	}
	if !reflect.DeepEqual(cfg.Settings.Observability, DefaultObservability) {
		t.Errorf("Observability = %+v, want DefaultObservability %+v", cfg.Settings.Observability, DefaultObservability)
	}
}

func TestDecodeSettings_eventBus(t *testing.T) {
	tests := []struct {
		name string
		body string
		want EventBus
	}{
		{"declared", `
  event_bus {
    subscribe_queue_bound = 4096
  }`, EventBus{SubscribeQueueBound: 4096}},
		// event_bus{} declares exactly one attribute, so an empty block is
		// the only "partial" case there is — and it is indistinguishable
		// from an absent block by design (no all-or-nothing rule applies).
		{"empty block", `
  event_bus {
  }`, DefaultEventBus},
		{"block absent", "", DefaultEventBus},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, settingsHCL(tt.body))
			cfg, err := LoadFile(context.Background(), testProvider(t), path)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if cfg.Settings.EventBus != tt.want {
				t.Fatalf("EventBus = %+v, want %+v", cfg.Settings.EventBus, tt.want)
			}
		})
	}
}

func TestDecodeSettings_doomLoop(t *testing.T) {
	tests := []struct {
		name string
		body string
		want DoomLoopSettings
	}{
		{"both attributes", `
  doom_loop {
    window_size = 12
    threshold   = 5
  }`, DoomLoopSettings{WindowSize: 12, Threshold: 5}},
		{"window_size only", `
  doom_loop {
    window_size = 12
  }`, DoomLoopSettings{WindowSize: 12, Threshold: DefaultDoomLoopSettings.Threshold}},
		{"threshold only", `
  doom_loop {
    threshold = 4
  }`, DoomLoopSettings{WindowSize: DefaultDoomLoopSettings.WindowSize, Threshold: 4}},
		{"empty block", `
  doom_loop {
  }`, DefaultDoomLoopSettings},
		{"block absent", "", DefaultDoomLoopSettings},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, settingsHCL(tt.body))
			cfg, err := LoadFile(context.Background(), testProvider(t), path)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if cfg.Settings.DoomLoop != tt.want {
				t.Fatalf("DoomLoop = %+v, want %+v", cfg.Settings.DoomLoop, tt.want)
			}
		})
	}
}

// TestDecodeSettings_defaultDoomLoopMatchesDoomloopPackage locks in the
// single-source-of-truth rule: DefaultDoomLoopSettings is derived from
// doomloop.DefaultConfig, never a second copy of the numbers.
func TestDecodeSettings_defaultDoomLoopMatchesDoomloopPackage(t *testing.T) {
	t.Parallel()

	if DefaultDoomLoopSettings.WindowSize != doomloop.DefaultConfig.WindowSize {
		t.Errorf("WindowSize = %d, want doomloop.DefaultConfig.WindowSize %d",
			DefaultDoomLoopSettings.WindowSize, doomloop.DefaultConfig.WindowSize)
	}
	if DefaultDoomLoopSettings.Threshold != doomloop.DefaultConfig.Threshold {
		t.Errorf("Threshold = %d, want doomloop.DefaultConfig.Threshold %d",
			DefaultDoomLoopSettings.Threshold, doomloop.DefaultConfig.Threshold)
	}
}

func TestDecodeSettings_flatOptionalAttrs(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantHookMS   int
		wantToolMS   int
		wantMaxDepth *int
	}{
		{"all declared", `
  default_hook_timeout_ms = 1500
  default_tool_timeout_ms = 60000
  max_depth               = 4`, 1500, 60000, ptr(4)},
		{"all omitted", "", DefaultHookTimeoutMS, DefaultToolTimeoutMS, nil},
		{"only hook timeout declared", `
  default_hook_timeout_ms = 250`, 250, DefaultToolTimeoutMS, nil},
		// An explicit max_depth = 0 is a real, declarable choice ("this
		// root session may spawn nothing"), semantically distinct from
		// unset — which is exactly why the field is a *int.
		{"max_depth explicitly zero", `
  max_depth = 0`, DefaultHookTimeoutMS, DefaultToolTimeoutMS, ptr(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, settingsHCL(tt.body))
			cfg, err := LoadFile(context.Background(), testProvider(t), path)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if got := cfg.Settings.DefaultHookTimeoutMS; got != tt.wantHookMS {
				t.Errorf("DefaultHookTimeoutMS = %d, want %d", got, tt.wantHookMS)
			}
			if got := cfg.Settings.DefaultToolTimeoutMS; got != tt.wantToolMS {
				t.Errorf("DefaultToolTimeoutMS = %d, want %d", got, tt.wantToolMS)
			}
			got := cfg.Settings.MaxDepth
			switch {
			case tt.wantMaxDepth == nil && got != nil:
				t.Errorf("MaxDepth = %d, want nil", *got)
			case tt.wantMaxDepth != nil && got == nil:
				t.Errorf("MaxDepth = nil, want pointer to %d", *tt.wantMaxDepth)
			case tt.wantMaxDepth != nil && *got != *tt.wantMaxDepth:
				t.Errorf("MaxDepth = %d, want %d", *got, *tt.wantMaxDepth)
			}
		})
	}
}
