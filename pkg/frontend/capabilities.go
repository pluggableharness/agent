package frontend

import (
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// CapabilitiesOption configures one optional field of a Capabilities built
// by NewCapabilities.
type CapabilitiesOption func(*Capabilities)

// WithSlashCommands sets the prompt-expansion commands this frontend
// itself contributes.
func WithSlashCommands(commands ...*commonv1.PromptExpansionSpec) CapabilitiesOption {
	return func(c *Capabilities) { c.SlashCommands = commands }
}

// WithSupportedRegions sets the Regions this frontend proactively declares
// it can render into.
func WithSupportedRegions(regions ...renderv1.Region) CapabilitiesOption {
	return func(c *Capabilities) { c.SupportedRegions = regions }
}

// WithSupportedHookPoints sets the hook points this frontend can
// subscribe to.
func WithSupportedHookPoints(points ...commonv1.HookPoint) CapabilitiesOption {
	return func(c *Capabilities) { c.SupportedHookPoints = points }
}

// NewCapabilities builds a Capabilities from schema — typically assembled
// with github.com/pluggableharness/agent/pkg/config's Schema and Attribute
// — plus any options. schema MAY be nil for a provider with no
// configuration surface at all.
func NewCapabilities(schema *configv1.ConfigSchema, opts ...CapabilitiesOption) *Capabilities {
	c := &Capabilities{ConfigSchema: schema}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// capabilitiesToProto converts c into the generated response type
// GetCapabilities returns. A nil c converts to an empty
// FrontendCapabilities rather than a nil pointer, since
// GetCapabilitiesResponse.Capabilities is not itself optional on the wire.
func capabilitiesToProto(c *Capabilities) *frontendv1.FrontendCapabilities {
	if c == nil {
		return &frontendv1.FrontendCapabilities{}
	}
	return &frontendv1.FrontendCapabilities{
		SlashCommands:       c.SlashCommands,
		ConfigSchema:        c.ConfigSchema,
		SupportedRegions:    c.SupportedRegions,
		SupportedHookPoints: c.SupportedHookPoints,
	}
}
