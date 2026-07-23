package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pluggableharness/agent/internal/policy"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func writeHCL(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.hcl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}
	return path
}

const minimalValidHCL = `
required_providers {
  anthropic = {
    source  = "github.com/agentco/provider-anthropic"
    version = "~> 1.2.3"
  }
}

provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}

policy "auto_approve_reads" {
  match  = { kind = "data_source" }
  action = "allow"
}

agent_profile "default" {
  model {
    primary {
      provider = "anthropic"
      id       = "claude-opus-4-8"
    }
    fallback {
      provider = "anthropic"
      id       = "claude-sonnet-5"
    }
  }

  max_turns        = 200
  max_cost_usd     = 5.00
  max_wall_clock_s = 3600
}

hook "post-tool-call" {
  provider = "audit-logger"
  mode     = "observe"
}

settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = false

  retry {
    base_delay_ms  = 500
    backoff_factor = 2
    max_retries    = 5
  }
}
`

func TestLoadFile_minimalValid(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-123")
	path := writeHCL(t, minimalValidHCL)

	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}

	rp, ok := cfg.RequiredProviders["anthropic"]
	if !ok {
		t.Fatal("RequiredProviders[anthropic] missing")
	}
	if rp.Source != "github.com/agentco/provider-anthropic" || rp.Constraint != "~> 1.2.3" {
		t.Fatalf("required provider = %+v, unexpected", rp)
	}

	if _, ok := cfg.ProviderBodies["anthropic"]; !ok {
		t.Fatal("ProviderBodies[anthropic] missing")
	}

	if len(cfg.Policies) != 1 || cfg.Policies[0].Name != "auto_approve_reads" {
		t.Fatalf("Policies = %+v, unexpected", cfg.Policies)
	}

	profile, ok := cfg.AgentProfiles["default"]
	if !ok {
		t.Fatal("AgentProfiles[default] missing")
	}
	if profile.Model.Primary.ID != "claude-opus-4-8" {
		t.Fatalf("profile.Model.Primary = %+v, unexpected", profile.Model.Primary)
	}
	if len(profile.Model.Fallbacks) != 1 || profile.Model.Fallbacks[0].ID != "claude-sonnet-5" {
		t.Fatalf("profile.Model.Fallbacks = %+v, unexpected", profile.Model.Fallbacks)
	}
	if profile.MaxTurns != 200 || profile.MaxCostUSD != 5.00 || profile.MaxWallClockS != 3600 {
		t.Fatalf("profile loop bounds = %+v, unexpected", profile)
	}

	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Point != "post-tool-call" || cfg.Hooks[0].Mode != "observe" {
		t.Fatalf("Hooks = %+v, unexpected", cfg.Hooks)
	}

	if cfg.Settings.DefaultFrontend != "tui" || cfg.Settings.LogLevel != "info" {
		t.Fatalf("Settings = %+v, unexpected", cfg.Settings)
	}
	if cfg.Settings.Retry != (RetrySettings{BaseDelayMS: 500, BackoffFactor: 2, MaxRetries: 5}) {
		t.Fatalf("Settings.Retry = %+v, unexpected", cfg.Settings.Retry)
	}
}

func TestLoadFile_settingsDefaultsWhenAbsent(t *testing.T) {
	path := writeHCL(t, `
required_providers {
  anthropic = { source = "github.com/agentco/provider-anthropic", version = "~> 1.0" }
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}
	if cfg.Settings.Retry != DefaultRetrySettings {
		t.Fatalf("Settings.Retry = %+v, want DefaultRetrySettings %+v", cfg.Settings.Retry, DefaultRetrySettings)
	}
	if !reflect.DeepEqual(cfg.Settings.Observability, DefaultObservability) {
		t.Fatalf("Settings.Observability = %+v, want DefaultObservability %+v", cfg.Settings.Observability, DefaultObservability)
	}
}

func TestLoadFile_unknownTopLevelBlock(t *testing.T) {
	path := writeHCL(t, `
session {
  max_turns = 10
}
`)
	if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
		t.Fatal("LoadFile: want error for unknown top-level block, got nil")
	}
}

func TestLoadFile_duplicateSettings(t *testing.T) {
	path := writeHCL(t, `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = false
}
settings {
  default_frontend = "tui"
  log_level        = "warn"
  telemetry        = true
}
`)
	_, err := LoadFile(context.Background(), testProvider(t), path)
	if err == nil {
		t.Fatal("LoadFile: want error for duplicate settings block, got nil")
	}
	if !errors.Is(err, ErrDuplicateBlock) {
		t.Fatalf("LoadFile error = %v, want wrapping ErrDuplicateBlock", err)
	}
}

func TestLoadFile_duplicateRequiredProviders(t *testing.T) {
	path := writeHCL(t, `
required_providers {
  anthropic = { source = "github.com/agentco/provider-anthropic", version = "~> 1.0" }
}
required_providers {
  filesystem = { source = "github.com/agentco/provider-filesystem", version = "~> 1.0" }
}
`)
	_, err := LoadFile(context.Background(), testProvider(t), path)
	if !errors.Is(err, ErrDuplicateBlock) {
		t.Fatalf("LoadFile error = %v, want wrapping ErrDuplicateBlock", err)
	}
}

func TestLoadFile_duplicateProviderName(t *testing.T) {
	path := writeHCL(t, `
provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}
provider "anthropic" {
  api_key = env("ANTHROPIC_API_KEY")
}
`)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	_, err := LoadFile(context.Background(), testProvider(t), path)
	if !errors.Is(err, ErrDuplicateBlock) {
		t.Fatalf("LoadFile error = %v, want wrapping ErrDuplicateBlock", err)
	}
}

func TestLoadFile_policyConflictDetected(t *testing.T) {
	path := writeHCL(t, `
policy "a" {
  match  = { provider = "filesystem", kind = "resource" }
  action = "allow"
}
policy "b" {
  match  = { provider = "filesystem", kind = "resource" }
  action = "deny"
}
`)
	_, err := LoadFile(context.Background(), testProvider(t), path)
	if err == nil {
		t.Fatal("LoadFile: want error for conflicting policy rules, got nil")
	}
}

func TestLoadFile_policyNonConflictingSameTuple(t *testing.T) {
	// Same specificity tuple (both set only tool_name), but disjoint
	// values — must NOT be flagged as a conflict (the §7.2 correction).
	path := writeHCL(t, `
policy "read" {
  match  = { tool_name = "read_file" }
  action = "allow"
}
policy "write" {
  match  = { tool_name = "write_file" }
  action = "ask"
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error for non-conflicting rules: %v", err)
	}
	if len(cfg.Policies) != 2 {
		t.Fatalf("len(Policies) = %d, want 2", len(cfg.Policies))
	}
}

func TestLoadFile_policyRiskAndKindValues(t *testing.T) {
	path := writeHCL(t, `
policy "block_high_risk" {
  match  = { risk = "critical" }
  action = "deny"
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}
	rule := cfg.Policies[0]
	if rule.Match.Risk == nil || *rule.Match.Risk != toolv1.RiskClass_RISK_CLASS_CRITICAL {
		t.Fatalf("rule.Match.Risk = %v, want RISK_CLASS_CRITICAL", rule.Match.Risk)
	}
}

func TestLoadFile_invalidPolicyAction(t *testing.T) {
	path := writeHCL(t, `
policy "bad" {
  match  = { kind = "resource" }
  action = "maybe"
}
`)
	if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
		t.Fatal("LoadFile: want error for invalid policy action, got nil")
	}
}

func TestLoadFile_invalidHookMode(t *testing.T) {
	path := writeHCL(t, `
hook "post-tool-call" {
  provider = "audit-logger"
  mode     = "not-a-real-mode"
}
`)
	if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
		t.Fatal("LoadFile: want error for invalid hook mode, got nil")
	}
}

func TestLoadFile_agentProfileMissingPrimary(t *testing.T) {
	path := writeHCL(t, `
agent_profile "broken" {
  model {
    fallback {
      provider = "anthropic"
      id       = "claude-haiku-4-5"
    }
  }
}
`)
	if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
		t.Fatal("LoadFile: want error for a model{} block with no primary, got nil")
	}
}

func TestLoadFile_maxDepthPointer(t *testing.T) {
	path := writeHCL(t, `
agent_profile "code-reviewer" {
  model {
    primary {
      provider = "anthropic"
      id       = "claude-sonnet-5"
    }
  }
  max_depth = 1
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}
	profile := cfg.AgentProfiles["code-reviewer"]
	if profile.MaxDepth == nil || *profile.MaxDepth != 1 {
		t.Fatalf("profile.MaxDepth = %v, want pointer to 1", profile.MaxDepth)
	}
}

func TestLoadFile_toolsAndSlashCommands(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	path := writeHCL(t, `
agent_profile "code-reviewer" {
  model {
    primary {
      provider = "anthropic"
      id       = "claude-sonnet-5"
    }
  }

  tools = [
    "filesystem.read_file",
    "search.*",
  ]

  slash_commands = ["compact"]
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}
	profile := cfg.AgentProfiles["code-reviewer"]
	wantTools := []string{"filesystem.read_file", "search.*"}
	if len(profile.Tools) != len(wantTools) || profile.Tools[0] != wantTools[0] || profile.Tools[1] != wantTools[1] {
		t.Fatalf("profile.Tools = %v, want %v", profile.Tools, wantTools)
	}
	if len(profile.SlashCommands) != 1 || profile.SlashCommands[0] != "compact" {
		t.Fatalf("profile.SlashCommands = %v, want [compact]", profile.SlashCommands)
	}
}

func TestLoadFile_allRiskClassValues(t *testing.T) {
	tests := []struct {
		hclValue string
		want     toolv1.RiskClass
	}{
		{"read_only", toolv1.RiskClass_RISK_CLASS_READ_ONLY},
		{"low", toolv1.RiskClass_RISK_CLASS_LOW},
		{"moderate", toolv1.RiskClass_RISK_CLASS_MODERATE},
		{"high", toolv1.RiskClass_RISK_CLASS_HIGH},
		{"critical", toolv1.RiskClass_RISK_CLASS_CRITICAL},
	}
	for _, tt := range tests {
		t.Run(tt.hclValue, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, `
policy "p" {
  match  = { risk = "`+tt.hclValue+`" }
  action = "deny"
}
`)
			cfg, err := LoadFile(context.Background(), testProvider(t), path)
			if err != nil {
				t.Fatalf("LoadFile: unexpected error: %v", err)
			}
			if *cfg.Policies[0].Match.Risk != tt.want {
				t.Fatalf("risk = %v, want %v", *cfg.Policies[0].Match.Risk, tt.want)
			}
		})
	}

	t.Run("invalid risk value", func(t *testing.T) {
		t.Parallel()
		path := writeHCL(t, `
policy "p" {
  match  = { risk = "extreme" }
  action = "deny"
}
`)
		_, err := LoadFile(context.Background(), testProvider(t), path)
		if !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("LoadFile error = %v, want wrapping ErrInvalidValue", err)
		}
	})
}

func TestLoadFile_policyKindResource(t *testing.T) {
	path := writeHCL(t, `
policy "p" {
  match  = { kind = "resource" }
  action = "ask"
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}
	if *cfg.Policies[0].Match.Kind != policy.KindResource {
		t.Fatalf("kind = %v, want KindResource", *cfg.Policies[0].Match.Kind)
	}
}

func TestLoadFile_settingsWrongTypes(t *testing.T) {
	tests := []struct {
		name string
		hcl  string
	}{
		{"telemetry not a bool", `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = "not-a-bool"
}
`},
		{"invalid log_level", `
settings {
  default_frontend = "tui"
  log_level        = "verbose"
  telemetry        = false
}
`},
		{"retry base_delay_ms not a number", `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = false
  retry {
    base_delay_ms  = "fast"
    backoff_factor = 2
    max_retries    = 5
  }
}
`},
		{"observability invalid protocol", `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true
  observability {
    endpoint            = "localhost:4317"
    protocol            = "carrier-pigeon"
    sampling_ratio      = 1.0
    traces_enabled      = true
    metrics_enabled     = true
    logs_enabled        = true
    export_interval_ms  = 10000
    service_name        = "pluggableharness-agent"
  }
}
`},
		{"observability sampling_ratio not a number", `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true
  observability {
    endpoint            = "localhost:4317"
    protocol            = "grpc"
    sampling_ratio      = "all-of-it"
    traces_enabled      = true
    metrics_enabled     = true
    logs_enabled        = true
    export_interval_ms  = 10000
    service_name        = "pluggableharness-agent"
  }
}
`},
		{"observability resource_attrs not an object", `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true
  observability {
    endpoint            = "localhost:4317"
    protocol            = "grpc"
    sampling_ratio      = 1.0
    traces_enabled      = true
    metrics_enabled     = true
    logs_enabled        = true
    export_interval_ms  = 10000
    service_name        = "pluggableharness-agent"
    resource_attrs      = "not-an-object"
  }
}
`},
		{"observability logs_enabled not a bool", `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = true
  observability {
    endpoint            = "localhost:4317"
    protocol            = "grpc"
    sampling_ratio      = 1.0
    traces_enabled      = true
    metrics_enabled     = true
    logs_enabled        = "not-a-bool"
    export_interval_ms  = 10000
    service_name        = "pluggableharness-agent"
  }
}
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, tt.hcl)
			if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
				t.Fatalf("LoadFile: want error for %s, got nil", tt.name)
			}
		})
	}
}

func TestLoadFile_requiredProvidersErrors(t *testing.T) {
	tests := []struct {
		name string
		hcl  string
	}{
		{"missing source", `
required_providers {
  anthropic = { version = "~> 1.0" }
}
`},
		{"missing version", `
required_providers {
  anthropic = { source = "github.com/agentco/provider-anthropic" }
}
`},
		{"not an object", `
required_providers {
  anthropic = "not-an-object"
}
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, tt.hcl)
			if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
				t.Fatalf("LoadFile: want error for %s, got nil", tt.name)
			}
		})
	}
}

func TestLoadFile_modelRefMissingID(t *testing.T) {
	path := writeHCL(t, `
agent_profile "broken" {
  model {
    primary {
      provider = "anthropic"
    }
  }
}
`)
	if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
		t.Fatal("LoadFile: want error for a model ref missing id, got nil")
	}
}

func TestLoadFile_hookErrors(t *testing.T) {
	tests := []struct {
		name string
		hcl  string
	}{
		{"missing provider", `
hook "post-tool-call" {
  mode = "observe"
}
`},
		{"missing mode", `
hook "post-tool-call" {
  provider = "audit-logger"
}
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHCL(t, tt.hcl)
			if _, err := LoadFile(context.Background(), testProvider(t), path); err == nil {
				t.Fatalf("LoadFile: want error for %s, got nil", tt.name)
			}
		})
	}
}
