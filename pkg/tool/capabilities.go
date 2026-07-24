package tool

import (
	"context"
	"fmt"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// BuildGetSchemaResponse assembles the full GetSchemaResponse for p: every
// operation's Schema (via p.Schema, converted and validated with
// toProtoSchema), plus this provider's config schema, slash commands,
// and supported hook points when p additionally implements
// ConfigSchemaProvider, SlashCommandProvider, or HookPointProvider — see
// tool.go. This package does not re-validate a ConfigSchemaProvider's
// output; it trusts pkg/config's own Attribute/Schema validation, which a
// well-behaved ConfigSchemaProvider implementation is expected to have run
// already.
func BuildGetSchemaResponse(ctx context.Context, p Provider) (*toolv1.GetSchemaResponse, error) {
	schemas, err := p.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("tool: get schema: %w", err)
	}

	tools := make([]*toolv1.ToolSchema, 0, len(schemas))
	for _, s := range schemas {
		ps, err := toProtoSchema(s)
		if err != nil {
			return nil, fmt.Errorf("tool: get schema: %w", err)
		}
		tools = append(tools, ps)
	}

	resp := &toolv1.GetSchemaResponse{Tools: tools}

	if cs, ok := p.(ConfigSchemaProvider); ok {
		schema, err := cs.ConfigSchema()
		if err != nil {
			return nil, fmt.Errorf("tool: get schema: config schema: %w", err)
		}
		resp.ConfigSchema = schema
	}
	if sc, ok := p.(SlashCommandProvider); ok {
		resp.SlashCommands = sc.SlashCommands()
	}
	if hp, ok := p.(HookPointProvider); ok {
		resp.SupportedHookPoints = hp.SupportedHookPoints()
	}

	return resp, nil
}
