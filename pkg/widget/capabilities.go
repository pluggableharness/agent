package widget

import (
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// NewCapabilities builds a Capabilities from a config schema built with
// pkg/config (Schema and Attribute), the regions this widget intends to
// contribute to, and the hook points it can subscribe to in observe mode.
// It is a plain constructor, not a validating one — pkg/config.Schema
// already validates configSchema's own invariants before a caller has one
// to pass here, and Regions/SupportedHookPoints are enum lists with no
// further invariant of their own to check.
func NewCapabilities(configSchema *configv1.ConfigSchema, regions []renderv1.Region, hookPoints ...commonv1.HookPoint) Capabilities {
	return Capabilities{
		Regions:             regions,
		ConfigSchema:        configSchema,
		SupportedHookPoints: hookPoints,
	}
}
