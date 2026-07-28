package widget

import (
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// CapabilitiesOption configures one optional field of a Capabilities
// built by NewCapabilities.
type CapabilitiesOption func(*Capabilities)

// WithSupportedHookPoints sets the hook points this widget can
// subscribe to.
func WithSupportedHookPoints(points ...commonv1.HookPoint) CapabilitiesOption {
	return func(c *Capabilities) { c.SupportedHookPoints = points }
}

// NewCapabilities builds a Capabilities from schema plus any options.
// schema MAY be nil for a provider with no configuration surface.
func NewCapabilities(schema *configv1.ConfigSchema, opts ...CapabilitiesOption) Capabilities {
	c := Capabilities{ConfigSchema: schema}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// toProtoCapabilities converts c into the wire type GetCapabilities
// returns.
func toProtoCapabilities(c Capabilities) *widgetv1.WidgetCapabilities {
	return &widgetv1.WidgetCapabilities{
		ConfigSchema:        c.ConfigSchema,
		SupportedHookPoints: c.SupportedHookPoints,
	}
}
