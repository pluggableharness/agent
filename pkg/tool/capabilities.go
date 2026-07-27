package tool

import (
	"fmt"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// BuildGetSchemaResponse assembles the full GetSchemaResponse for p: every
// Tool's Schema (converted and validated with toProtoSchema), plus this
// provider's config schema, slash commands, and supported hook points when
// p additionally implements ConfigSchemaProvider, SlashCommandProvider, or
// HookPointProvider — see tool.go. This package does not re-validate a
// ConfigSchemaProvider's output; it trusts pkg/config's own
// Attribute/Schema validation, which a well-behaved ConfigSchemaProvider
// implementation is expected to have run already.
//
// Takes no context because every input to the advertisement is static —
// docs/specifications/tool/protocol.md#getschema's "MUST be cheaply
// re-queryable and MUST NOT require a network call", expressed in the
// signature rather than left to provider discipline.
func BuildGetSchemaResponse(p Provider) (*toolv1.GetSchemaResponse, error) {
	_, resp, err := resolveTools(p)
	return resp, err
}

// resolveTools walks p's Tools exactly once, returning both the name-keyed
// dispatch table Service routes an incoming Call with and the wire
// advertisement GetSchema serves. The two are built from that single pass
// deliberately: querying each Tool's Schema twice would let a provider whose
// declarations are not stable produce a dispatch table that disagrees with
// what it told the kernel it exposes.
func resolveTools(p Provider) (map[string]Tool, *toolv1.GetSchemaResponse, error) {
	implTools := p.Tools()

	byName := make(map[string]Tool, len(implTools))
	tools := make([]*toolv1.ToolSchema, 0, len(implTools))
	for i, t := range implTools {
		if t == nil {
			return nil, nil, fmt.Errorf("tool: get schema: tool at index %d: %w", i, ErrNilTool)
		}
		s, err := t.Schema()
		if err != nil {
			return nil, nil, fmt.Errorf("tool: get schema: tool at index %d: %w", i, err)
		}
		ps, err := toProtoSchema(s)
		if err != nil {
			return nil, nil, fmt.Errorf("tool: get schema: %w", err)
		}
		if _, dup := byName[s.Name]; dup {
			return nil, nil, fmt.Errorf("tool: get schema: %q: %w", s.Name, ErrDuplicateToolName)
		}
		byName[s.Name] = t
		tools = append(tools, ps)
	}

	resp := &toolv1.GetSchemaResponse{Tools: tools}

	if cs, ok := p.(ConfigSchemaProvider); ok {
		schema, err := cs.ConfigSchema()
		if err != nil {
			return nil, nil, fmt.Errorf("tool: get schema: config schema: %w", err)
		}
		resp.ConfigSchema = schema
	}
	if sc, ok := p.(SlashCommandProvider); ok {
		resp.SlashCommands = sc.SlashCommands()
	}
	if hp, ok := p.(HookPointProvider); ok {
		resp.SupportedHookPoints = hp.SupportedHookPoints()
	}

	return byName, resp, nil
}
