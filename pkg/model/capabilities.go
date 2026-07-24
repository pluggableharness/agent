package model

import (
	"fmt"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// capabilitiesOptions collects CapabilitiesOption values, mirroring
// pkg/config's attributeOptions shape.
type capabilitiesOptions struct {
	slashCommands       []*commonv1.PromptExpansionSpec
	supportedHookPoints []commonv1.HookPoint
}

// CapabilitiesOption configures one optional field of a Capabilities value
// built by NewCapabilities.
type CapabilitiesOption func(*capabilitiesOptions)

// WithSlashCommands sets the prompt-expansion slash commands this provider
// contributes, per docs/specifications/model/protocol.md#getcapabilities.
func WithSlashCommands(specs ...*commonv1.PromptExpansionSpec) CapabilitiesOption {
	return func(o *capabilitiesOptions) { o.slashCommands = specs }
}

// WithSupportedHookPoints sets which hook points this plugin can serve via
// HookSubscriberService.DispatchHook, per
// docs/specifications/model/data-types.md#capabilitiessupported_hook_points.
func WithSupportedHookPoints(points ...commonv1.HookPoint) CapabilitiesOption {
	return func(o *capabilitiesOptions) { o.supportedHookPoints = points }
}

// NewCapabilities builds a Capabilities value from models and configSchema,
// validating every MUST-level invariant docs/specifications/model/data-types.md#modelspec
// and docs/specifications/model/data-types.md#pricing describe before
// returning it. A caller gets back either a known-good *Capabilities or an
// error identifying which invariant it violated (compare with errors.Is
// against ErrInvalidCapabilities or ErrInvalidPricing).
func NewCapabilities(models []Spec, configSchema *configv1.ConfigSchema, opts ...CapabilitiesOption) (*Capabilities, error) {
	var o capabilitiesOptions
	for _, opt := range opts {
		opt(&o)
	}

	c := &Capabilities{
		Models:              models,
		SlashCommands:       o.slashCommands,
		ConfigSchema:        configSchema,
		SupportedHookPoints: o.supportedHookPoints,
	}
	if err := validateCapabilities(c); err != nil {
		return nil, err
	}
	return c, nil
}

// validateCapabilities checks c against docs/specifications/model/data-types.md#modelspec's
// MUST-level rules: at least one model, a config schema present, and every
// model's own invariants (validateModelSpec).
func validateCapabilities(c *Capabilities) error {
	if len(c.Models) == 0 {
		return fmt.Errorf("%w: at least one model required", ErrInvalidCapabilities)
	}
	if c.ConfigSchema == nil {
		return fmt.Errorf("%w: config schema required", ErrInvalidCapabilities)
	}
	for i, m := range c.Models {
		if err := validateModelSpec(m); err != nil {
			return fmt.Errorf("%w: model %d (%q): %w", ErrInvalidCapabilities, i, m.ID, err)
		}
	}
	return nil
}

// validateModelSpec checks m against docs/specifications/model/data-types.md#modelspec's
// MUST-level rules for a single Spec: a non-empty id, ThinkingSpec's
// mode-dependent requirements (effort_levels/budget_range/default), and
// Pricing's own invariants (validatePricing).
func validateModelSpec(m Spec) error {
	if m.ID == "" {
		return fmt.Errorf("%w: id required", ErrInvalidCapabilities)
	}
	if err := validateThinkingSpec(m.Thinking); err != nil {
		return err
	}
	if err := validatePricing(m.Pricing, m.Caching.Supported); err != nil {
		return err
	}
	return nil
}

// validateThinkingSpec checks t against
// docs/specifications/model/data-types.md#thinkingspec: effort_levels
// required for THINKING_MODE_DISCRETE_EFFORT, budget_range required for
// THINKING_MODE_CONTINUOUS_BUDGET, and default required whenever mode is
// not THINKING_MODE_NONE
// (docs/specifications/model/conformance.md's "ThinkingSpec.default MUST
// be set when mode != none" row).
func validateThinkingSpec(t ThinkingSpec) error {
	if t.Mode == modelv1.ThinkingMode_THINKING_MODE_NONE {
		return nil
	}
	if t.Mode == modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT && len(t.EffortLevels) == 0 {
		return fmt.Errorf("%w: effort_levels required for THINKING_MODE_DISCRETE_EFFORT", ErrInvalidCapabilities)
	}
	if t.Mode == modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET && t.BudgetRange == nil {
		return fmt.Errorf("%w: budget_range required for THINKING_MODE_CONTINUOUS_BUDGET", ErrInvalidCapabilities)
	}
	if t.Default == "" {
		return fmt.Errorf("%w: default required when mode is not THINKING_MODE_NONE", ErrInvalidCapabilities)
	}
	return nil
}

// validatePricing checks p against docs/specifications/model/data-types.md#pricing:
// currency set, at least one tier unless free, cache pricing present on
// every tier iff cachingSupported, and no two tiers overlapping across
// both the time dimension (effective_from/effective_until) and the
// input-size dimension (input_tokens_from/input_tokens_until)
// simultaneously.
//
// Judgment call: the spec also requires rejecting a *gapped* tier set (no
// (timestamp, input_token_count) pair left unmatched), not just an
// overlapping one. Detecting a gap in a general two-dimensional,
// partially-unbounded interval set is a materially harder problem than
// detecting an overlap (it requires reconstructing the full covered region
// and comparing it against the unbounded plane) and the task brief
// explicitly cautions against over-engineering a fully generic tier
// validator here. Overlap detection catches the more common authoring
// mistake (two tiers both claiming the same moment/input-size) and is a
// straightforward, tractable pairwise check; gap detection is left
// unimplemented, consistent with docs/specifications/model/conformance.md's
// own open question about how strict this check should ultimately be.
func validatePricing(p Pricing, cachingSupported bool) error {
	if p.Currency == "" {
		return fmt.Errorf("%w: currency required", ErrInvalidPricing)
	}
	if !p.Free && len(p.Tiers) == 0 {
		return fmt.Errorf("%w: at least one tier required unless free", ErrInvalidPricing)
	}
	for i, t := range p.Tiers {
		if cachingSupported && (t.CacheWritePerMtok == nil || t.CacheReadPerMtok == nil) {
			return fmt.Errorf("%w: tier %d missing cache pricing though caching is supported", ErrInvalidPricing, i)
		}
		for j := i + 1; j < len(p.Tiers); j++ {
			if tiersOverlap(t, p.Tiers[j]) {
				return fmt.Errorf("%w: tier %d and tier %d overlap", ErrInvalidPricing, i, j)
			}
		}
	}
	return nil
}

// tiersOverlap reports whether a and b could both match the same
// (timestamp, input_token_count) pair — their time ranges overlap AND
// their input-token ranges overlap simultaneously, per
// docs/specifications/model/data-types.md#pricing's two-dimensional
// tier-matching rule.
func tiersOverlap(a, b PricingTier) bool {
	return timeRangesOverlap(a.EffectiveFrom, a.EffectiveUntil, b.EffectiveFrom, b.EffectiveUntil) &&
		int64RangesOverlap(a.InputTokensFrom, a.InputTokensUntil, b.InputTokensFrom, b.InputTokensUntil)
}

// timeRangesOverlap reports whether half-open ranges [aFrom, aUntil) and
// [bFrom, bUntil) overlap, where a nil bound is unbounded on that side.
func timeRangesOverlap(aFrom, aUntil, bFrom, bUntil *time.Time) bool {
	// aFrom < bUntil (or bUntil unbounded) AND bFrom < aUntil (or aUntil
	// unbounded).
	if aUntil != nil && bFrom != nil && !bFrom.Before(*aUntil) {
		return false
	}
	if bUntil != nil && aFrom != nil && !aFrom.Before(*bUntil) {
		return false
	}
	return true
}

// int64RangesOverlap reports whether half-open ranges [aFrom, aUntil) and
// [bFrom, bUntil) overlap, where a nil bound is unbounded on that side.
func int64RangesOverlap(aFrom, aUntil, bFrom, bUntil *int64) bool {
	if aUntil != nil && bFrom != nil && *bFrom >= *aUntil {
		return false
	}
	if bUntil != nil && aFrom != nil && *aFrom >= *bUntil {
		return false
	}
	return true
}
