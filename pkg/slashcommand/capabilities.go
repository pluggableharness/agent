package slashcommand

import (
	"context"
	"fmt"

	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
)

// BuildGetCapabilitiesResponse assembles the full GetCapabilitiesResponse
// for p: every command's Spec (via p.Capabilities, converted and validated
// with toProtoSpec), plus this provider's config schema and supported hook
// points when p additionally implements ConfigSchemaProvider or
// HookPointProvider — see slashcommand.go. Unlike pkg/tool's
// BuildGetSchemaResponse, there is no SlashCommandProvider-equivalent
// optional interface here: a SlashCommandSpec has no output_schema and
// this category does not itself declare PromptExpansionSpec entries (see
// docs/specifications/slashcommand/data-types.md#slashcommandspec-vs-promptexpansionspec).
// This package does not re-validate a ConfigSchemaProvider's output; it
// trusts pkg/config's own Attribute/Schema validation, which a
// well-behaved ConfigSchemaProvider implementation is expected to have run
// already.
func BuildGetCapabilitiesResponse(ctx context.Context, p Provider) (*slashcommandv1.GetCapabilitiesResponse, error) {
	specs, err := p.Capabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("slashcommand: get capabilities: %w", err)
	}

	commands := make([]*slashcommandv1.SlashCommandSpec, 0, len(specs))
	for _, s := range specs {
		ps, err := toProtoSpec(s)
		if err != nil {
			return nil, fmt.Errorf("slashcommand: get capabilities: %w", err)
		}
		commands = append(commands, ps)
	}

	resp := &slashcommandv1.GetCapabilitiesResponse{Commands: commands}

	if cs, ok := p.(ConfigSchemaProvider); ok {
		schema, err := cs.ConfigSchema()
		if err != nil {
			return nil, fmt.Errorf("slashcommand: get capabilities: config schema: %w", err)
		}
		resp.ConfigSchema = schema
	}
	if hp, ok := p.(HookPointProvider); ok {
		resp.SupportedHookPoints = hp.SupportedHookPoints()
	}

	return resp, nil
}
