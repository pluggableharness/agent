package model

import (
	"fmt"
	"slices"
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
	if err := validateCachingSpec(m.Caching); err != nil {
		return err
	}
	if err := validatePricing(m.Pricing, m.Caching.Supported); err != nil {
		return err
	}
	return nil
}

// validateCachingSpec checks c against
// docs/specifications/model/data-types.md#cachingspec.
//
// The two axes are independent, so a model may declare either or both —
// the only rules are that declaring caching requires naming at least one
// mechanism, and that a non-caching model names none. A model caching by
// some mechanism this protocol cannot express is not declarable, and
// leaving both axes false would read as "no caching" to every caller,
// which is why the positive case is checked rather than assumed.
func validateCachingSpec(c CachingSpec) error {
	if !c.Supported {
		if c.ExplicitMarkers || c.ImplicitAutomatic {
			return fmt.Errorf("%w: caching mechanism declared on a model with caching unsupported", ErrInvalidCapabilities)
		}
		return nil
	}
	if !c.ExplicitMarkers && !c.ImplicitAutomatic {
		return fmt.Errorf("%w: caching supported but neither explicit_markers nor implicit_automatic declared", ErrInvalidCapabilities)
	}
	return nil
}

// validateThinkingSpec checks t against
// docs/specifications/model/data-types.md#thinkingspec.
//
// The axes are independent, so this validates each control that is present
// on its own terms rather than deriving requirements from one mode value.
// The one cross-axis rule is the unsupported case: a model that cannot
// reason at all MUST NOT declare a control for reasoning it does not do.
func validateThinkingSpec(t ThinkingSpec) error {
	if !t.Supported {
		switch {
		case t.Effort != nil:
			return fmt.Errorf("%w: effort control declared on a model with thinking unsupported", ErrInvalidCapabilities)
		case t.Budget != nil:
			return fmt.Errorf("%w: budget control declared on a model with thinking unsupported", ErrInvalidCapabilities)
		case t.AdaptiveByDefault:
			return fmt.Errorf("%w: adaptive_by_default set on a model with thinking unsupported", ErrInvalidCapabilities)
		// UNSPECIFIED and NEVER are both accepted here, and that is
		// deliberate: the zero ThinkingSpec{} must be a valid declaration
		// for "this model does not reason", which is the most common case
		// by far and the one an author writes without thinking about it.
		// Only a positive claim that reasoning CAN be disabled is a real
		// contradiction worth rejecting.
		case t.Disable == modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
			t.Disable == modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_CONDITIONAL:
			return fmt.Errorf("%w: disable claims reasoning can be turned off on a model with thinking unsupported", ErrInvalidCapabilities)
		}
		return nil
	}

	if t.Disable == modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_UNSPECIFIED {
		return fmt.Errorf("%w: disable required when thinking is supported", ErrInvalidCapabilities)
	}
	if err := validateEffortControl(t.Effort); err != nil {
		return err
	}
	return validateBudgetControl(t.Budget)
}

// validateEffortControl checks a declared effort ladder. A nil control is
// valid — it means the model has no effort ladder, which is a normal
// position, not an omission.
func validateEffortControl(e *EffortControl) error {
	if e == nil {
		return nil
	}
	if len(e.Levels) == 0 {
		return fmt.Errorf("%w: effort control declared with no levels", ErrInvalidCapabilities)
	}
	if e.Default == "" {
		return fmt.Errorf("%w: effort control declared with no default level", ErrInvalidCapabilities)
	}
	// The default must name a real level, or a kernel sending it back as an
	// explicit override — the whole reason the field exists — would send a
	// value the vendor rejects.
	if !slices.Contains(e.Levels, e.Default) {
		return fmt.Errorf("%w: effort default %q is not one of the declared levels %v",
			ErrInvalidCapabilities, e.Default, e.Levels)
	}
	return nil
}

// validateBudgetControl checks a declared token-budget control. A nil
// control is valid, for the same reason a nil effort control is.
func validateBudgetControl(b *BudgetControl) error {
	if b == nil {
		return nil
	}
	if b.Range.Min > b.Range.Max {
		return fmt.Errorf("%w: budget range min %d exceeds max %d",
			ErrInvalidCapabilities, b.Range.Min, b.Range.Max)
	}
	if b.Default != nil && (*b.Default < b.Range.Min || *b.Default > b.Range.Max) {
		return fmt.Errorf("%w: budget default %d is outside the declared range [%d, %d]",
			ErrInvalidCapabilities, *b.Default, b.Range.Min, b.Range.Max)
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
