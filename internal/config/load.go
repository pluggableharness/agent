package config

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// topLevelSchema enumerates the exact six block types agent.hcl MAY
// contain (configuration.md §3). Body.Content (not PartialContent) enforces
// this as a closed schema: any other top-level block is a diagnostic,
// satisfying §3's "any other label is a config-load-time error."
var topLevelSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "required_providers"},
		{Type: "provider", LabelNames: []string{"name"}},
		{Type: "policy", LabelNames: []string{"name"}},
		{Type: "agent_profile", LabelNames: []string{"name"}},
		{Type: "hook", LabelNames: []string{"point"}},
		{Type: "settings"},
	},
}

// LoadFile parses path as an agent.hcl file (configuration.md §2: exactly
// one file, project root, no multi-file merge). It performs file I/O, so
// per internal/CLAUDE.md it logs entry at DEBUG and wraps the operation in
// a telemetry span via prov.StartConfigLoad, ended with the call's error.
func LoadFile(ctx context.Context, prov *telemetry.Provider, path string) (_ *Config, err error) {
	ctx, span := prov.StartConfigLoad(ctx, path)
	defer func() { telemetry.EndSpan(span, err) }()
	slog.DebugContext(ctx, "config: loading file", "path", path)

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		err = fmt.Errorf("config: load %s: %w", path, diags)
		return nil, err
	}
	cfg, err := decode(file.Body)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func decode(body hcl.Body) (*Config, error) {
	content, diags := body.Content(topLevelSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("config: %w", diags)
	}

	cfg := &Config{
		RequiredProviders: map[string]RequiredProvider{},
		ProviderBodies:    map[string]hcl.Body{},
		ProviderRanges:    map[string]hcl.Range{},
		AgentProfiles:     map[string]agentprofile.AgentProfile{},
		Settings:          Settings{Retry: DefaultRetrySettings, Observability: DefaultObservability},
	}

	var sawSettings, sawRequiredProviders bool

	for _, block := range content.Blocks {
		switch block.Type {
		case "required_providers":
			if sawRequiredProviders {
				return nil, fmt.Errorf("config: %w: required_providers", ErrDuplicateBlock)
			}
			sawRequiredProviders = true
			rp, err := decodeRequiredProviders(block.Body)
			if err != nil {
				return nil, err
			}
			cfg.RequiredProviders = rp

		case "provider":
			name := block.Labels[0]
			if _, exists := cfg.ProviderBodies[name]; exists {
				return nil, fmt.Errorf("config: provider %q: %w", name, ErrDuplicateBlock)
			}
			cfg.ProviderBodies[name] = block.Body
			cfg.ProviderRanges[name] = block.DefRange

		case "policy":
			rule, err := decodePolicy(block.Labels[0], block.Body)
			if err != nil {
				return nil, err
			}
			cfg.Policies = append(cfg.Policies, rule)

		case "agent_profile":
			name := block.Labels[0]
			if _, exists := cfg.AgentProfiles[name]; exists {
				return nil, fmt.Errorf("config: agent_profile %q: %w", name, ErrDuplicateBlock)
			}
			profile, err := decodeAgentProfile(name, block.Body)
			if err != nil {
				return nil, err
			}
			cfg.AgentProfiles[name] = profile

		case "hook":
			hook, err := decodeHook(block.Labels[0], block.Body, block.DefRange)
			if err != nil {
				return nil, err
			}
			cfg.Hooks = append(cfg.Hooks, hook)

		case "settings":
			if sawSettings {
				return nil, fmt.Errorf("config: %w: settings", ErrDuplicateBlock)
			}
			sawSettings = true
			settings, err := decodeSettings(block.Body)
			if err != nil {
				return nil, err
			}
			cfg.Settings = settings
		}
	}

	if err := policy.ValidateRules(cfg.Policies); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return cfg, nil
}
