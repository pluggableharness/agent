package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/agentprofile"
)

var agentProfileSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "tools", Required: false},
		{Name: "slash_commands", Required: false},
		{Name: "max_turns", Required: false},
		{Name: "max_cost_usd", Required: false},
		{Name: "max_wall_clock_s", Required: false},
		{Name: "max_depth", Required: false},
		{Name: "max_concurrent_subagents", Required: false},
	},
	Blocks: []hcl.BlockHeaderSchema{{Type: "model"}},
}

var modelBlockSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "primary"},
		{Type: "fallback"},
	},
}

var modelRefSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "provider", Required: true},
		{Name: "id", Required: true},
	},
}

// decodeAgentProfile decodes an agent_profile "<name>" { ... } block
// (configuration.md §8) into internal/agentprofile.AgentProfile.
func decodeAgentProfile(name string, body hcl.Body) (agentprofile.AgentProfile, error) {
	content, diags := body.Content(agentProfileSchema)
	if diags.HasErrors() {
		return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: %w", name, diags)
	}

	profile := agentprofile.AgentProfile{Name: name}

	for _, block := range content.Blocks {
		if block.Type == "model" {
			model, err := decodeModelBlock(block.Body)
			if err != nil {
				return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: %w", name, err)
			}
			profile.Model = model
		}
	}

	if attr, ok := content.Attributes["tools"]; ok {
		tools, err := attrStringList(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: tools: %w", name, err)
		}
		profile.Tools = tools
	}
	if attr, ok := content.Attributes["slash_commands"]; ok {
		cmds, err := attrStringList(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: slash_commands: %w", name, err)
		}
		profile.SlashCommands = cmds
	}
	if attr, ok := content.Attributes["max_turns"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: max_turns: %w", name, err)
		}
		profile.MaxTurns = v
	}
	if attr, ok := content.Attributes["max_cost_usd"]; ok {
		v, err := attrFloat(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: max_cost_usd: %w", name, err)
		}
		profile.MaxCostUSD = v
	}
	if attr, ok := content.Attributes["max_wall_clock_s"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: max_wall_clock_s: %w", name, err)
		}
		profile.MaxWallClockS = v
	}
	if attr, ok := content.Attributes["max_depth"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: max_depth: %w", name, err)
		}
		profile.MaxDepth = &v
	}
	if attr, ok := content.Attributes["max_concurrent_subagents"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return agentprofile.AgentProfile{}, fmt.Errorf("config: agent_profile %q: max_concurrent_subagents: %w", name, err)
		}
		profile.MaxConcurrentSubagents = v
	}

	return profile, nil
}

func decodeModelBlock(body hcl.Body) (agentprofile.ModelBlock, error) {
	content, diags := body.Content(modelBlockSchema)
	if diags.HasErrors() {
		return agentprofile.ModelBlock{}, fmt.Errorf("model: %w", diags)
	}

	var block agentprofile.ModelBlock
	var sawPrimary bool
	for _, b := range content.Blocks {
		ref, err := decodeModelRef(b.Body)
		if err != nil {
			return agentprofile.ModelBlock{}, fmt.Errorf("model.%s: %w", b.Type, err)
		}
		switch b.Type {
		case "primary":
			if sawPrimary {
				return agentprofile.ModelBlock{}, fmt.Errorf("model: %w: primary", ErrDuplicateBlock)
			}
			sawPrimary = true
			block.Primary = ref
		case "fallback":
			block.Fallbacks = append(block.Fallbacks, ref)
		}
	}
	if !sawPrimary {
		return agentprofile.ModelBlock{}, fmt.Errorf("model: %w: primary", ErrMissingField)
	}
	return block, nil
}

func decodeModelRef(body hcl.Body) (agentprofile.ModelRef, error) {
	content, diags := body.Content(modelRefSchema)
	if diags.HasErrors() {
		return agentprofile.ModelRef{}, diags
	}
	provider, err := attrString(content.Attributes["provider"])
	if err != nil {
		return agentprofile.ModelRef{}, fmt.Errorf("provider: %w", err)
	}
	id, err := attrString(content.Attributes["id"])
	if err != nil {
		return agentprofile.ModelRef{}, fmt.Errorf("id: %w", err)
	}
	return agentprofile.ModelRef{Provider: provider, ID: id}, nil
}
