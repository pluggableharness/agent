package context

import (
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// capabilitiesOptions collects CapabilitiesOption values before
// NewCapabilities assembles them. Defaults match Capabilities' own
// wire defaults: not a compactor, no slash commands, no supported hook
// points.
type capabilitiesOptions struct {
	compactor           bool
	slashCommands       []*commonv1.PromptExpansionSpec
	supportedHookPoints []commonv1.HookPoint
}

// CapabilitiesOption configures one optional field of a
// Capabilities built by NewCapabilities.
type CapabilitiesOption func(*capabilitiesOptions)

// WithCompactor declares this provider a compactor
// (data-types.md#ordering--chaining): it MAY rewrite, merge, or drop
// other providers' sections in the chain it receives, and MAY receive
// Request.ConversationHistory and return
// Contribution.RewrittenHistory.
func WithCompactor() CapabilitiesOption {
	return func(o *capabilitiesOptions) { o.compactor = true }
}

// WithSlashCommands declares the prompt-expansion slash commands this
// provider contributes (protocol.md#getcapabilities).
func WithSlashCommands(specs ...*commonv1.PromptExpansionSpec) CapabilitiesOption {
	return func(o *capabilitiesOptions) { o.slashCommands = specs }
}

// WithSupportedHookPoints declares which hook points (beyond
// context-assemble itself) this provider subscribes
// HookSubscriberService.DispatchHook to.
func WithSupportedHookPoints(points ...commonv1.HookPoint) CapabilitiesOption {
	return func(o *capabilitiesOptions) { o.supportedHookPoints = points }
}

// NewCapabilities builds a *Capabilities for a Provider's
// GetCapabilities response. defaultTokenBudget and stability are the two
// MUST-set fields (protocol.md#getcapabilities); configSchema is this
// provider's agent.hcl config schema, typically built with
// pkg/config.Schema — pass an empty schema (pkg/config.Schema() with no
// attributes) for a provider with no configuration, never nil, so
// GetCapabilities' ConfigSchema field is always populated per
// protocol.md#getcapabilities ("MUST include the provider's
// ConfigSchema"). Optional properties (Compactor, SlashCommands,
// SupportedHookPoints) are set via CapabilitiesOption.
func NewCapabilities(defaultTokenBudget int64, stability Stability, configSchema *configv1.ConfigSchema, opts ...CapabilitiesOption) *Capabilities {
	var o capabilitiesOptions
	for _, opt := range opts {
		opt(&o)
	}
	return &Capabilities{
		DefaultTokenBudget:  defaultTokenBudget,
		Stability:           stability,
		Compactor:           o.compactor,
		SlashCommands:       o.slashCommands,
		ConfigSchema:        configSchema,
		SupportedHookPoints: o.supportedHookPoints,
	}
}
