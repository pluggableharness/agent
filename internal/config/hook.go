package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
)

var hookSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "provider", Required: true},
		{Name: "mode", Required: true},
		{Name: "timeout_ms", Required: false},
	},
}

var validHookModes = map[string]bool{"observe": true, "transform": true, "veto": true}

// decodeHook decodes an explicit hook "<point>" { provider = ..., mode = ... }
// block (configuration.md §8.6).
func decodeHook(point string, body hcl.Body, defRange hcl.Range) (Hook, error) {
	content, diags := body.Content(hookSchema)
	if diags.HasErrors() {
		return Hook{}, fmt.Errorf("config: hook %q: %w", point, diags)
	}

	provider, err := attrString(content.Attributes["provider"])
	if err != nil {
		return Hook{}, fmt.Errorf("config: hook %q: provider: %w", point, err)
	}
	mode, err := attrString(content.Attributes["mode"])
	if err != nil {
		return Hook{}, fmt.Errorf("config: hook %q: mode: %w", point, err)
	}
	if !validHookModes[mode] {
		return Hook{}, fmt.Errorf("config: hook %q: mode: %w: %q", point, ErrInvalidValue, mode)
	}

	hook := Hook{Point: point, Provider: provider, Mode: mode, Range: defRange}

	// timeout_ms is optional: absent leaves TimeoutMS nil, meaning the
	// caller falls back to Settings.DefaultHookTimeoutMS
	// (agent-loop/hook-dispatch.md#per-subscriber-timeout).
	if attr, ok := content.Attributes["timeout_ms"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return Hook{}, fmt.Errorf("config: hook %q: timeout_ms: %w", point, err)
		}
		hook.TimeoutMS = &v
	}

	return hook, nil
}
