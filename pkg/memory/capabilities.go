package memory

import (
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
)

// capabilitiesToProto converts caps into the wire
// memoryv1.MemoryCapabilities GetCapabilities returns
// (docs/specifications/memory/data-types.md#memorycapabilities). There is
// no reverse conversion: GetCapabilities only ever flows provider → kernel,
// never decoded back by this SDK.
func capabilitiesToProto(caps Capabilities) *memoryv1.MemoryCapabilities {
	types := make([]memoryv1.MemoryType, 0, len(caps.SupportedTypes))
	for _, t := range caps.SupportedTypes {
		types = append(types, toProtoMemoryType(t))
	}

	scopes := make([]memoryv1.MemoryScope, 0, len(caps.SupportedScopes))
	for _, s := range caps.SupportedScopes {
		scopes = append(scopes, toProtoMemoryScope(s))
	}

	return &memoryv1.MemoryCapabilities{
		DefaultTokenBudget:    caps.DefaultTokenBudget,
		SupportedTypes:        types,
		SupportedScopes:       scopes,
		RatificationSupported: caps.RatificationSupported,
		SlashCommands:         caps.SlashCommands,
		ConfigSchema:          caps.ConfigSchema,
		SupportedHookPoints:   caps.SupportedHookPoints,
	}
}
