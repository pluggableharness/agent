package modelrequest

import (
	"slices"

	"google.golang.org/protobuf/proto"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Params is the result of resolving a caller's requested
// *modelv1.GenerationParams against a resolved model's *modelv1.ModelSpec,
// per
// docs/specifications/model/protocol.md#generation-parameter-validation-and-capability-aware-routing.
// Every field on Resolved is guaranteed forwardable to the plugin as-is —
// nothing downstream needs to re-check thinking or tool_choice capability
// against the model again.
type Params struct {
	// Resolved is the accepted, possibly fallback-adjusted
	// GenerationParams — the real generated type, per go-layout.md's
	// "internal/ MUST consume the generated types directly" rule. Nil iff
	// the caller's req was nil.
	Resolved *modelv1.GenerationParams

	// FellBackThinking reports whether ValidateParams cleared
	// Resolved.thinking_effort or Resolved.thinking_budget_tokens because
	// it was out of range for the resolved model's ThinkingSpec. Callers
	// use this for logging/telemetry at the call site — this package
	// itself never logs, per its pure-domain exemption.
	FellBackThinking bool

	// FellBackToolChoice reports whether ValidateParams cleared
	// Resolved.tool_choice because its mode wasn't in the resolved
	// model's ModelSpec.supported_tool_choice_modes.
	FellBackToolChoice bool
}

// ValidateParams resolves req against spec, applying
// protocol.md#generation-parameter-validation-and-capability-aware-routing's
// fallback rules:
//
//   - Resolved.thinking_effort is cleared unless spec declares a
//     ThinkingSpec.effort control and the requested value appears in its
//     levels.
//   - Resolved.thinking_budget_tokens is cleared unless spec declares a
//     ThinkingSpec.budget control and the requested value falls within its
//     range (inclusive of both bounds).
//
// The two thinking controls are independent axes, so each is validated
// against the control that governs it. A model declaring both is legal and
// both params survive; a model declaring neither falls back on both.
//   - Resolved.tool_choice is cleared (equivalent to
//     TOOL_CHOICE_MODE_AUTO, i.e. omitting tool_choice entirely) unless
//     its mode is TOOL_CHOICE_MODE_AUTO — which never needs a capability
//     check, since it's already the fallback target and every vendor
//     supports "the model decides freely" — or appears in
//     spec.GetSupportedToolChoiceModes().
//
// ValidateParams never returns an error for an out-of-range thinking or
// tool_choice param: per the spec's "reject or fall back" framing, this
// package always chooses fallback, guaranteeing nothing invalid reaches
// the wire. A caller wanting reject-instead-of-fallback as a stricter
// policy is free to inspect FellBackThinking/FellBackToolChoice on the
// returned Params and fail the turn itself.
//
// req and spec are read-only; ValidateParams never mutates either. A nil
// req resolves to a nil Params.Resolved with both fallback flags false —
// there is nothing to validate when every param already takes its
// model-specific default. A nil spec is treated as a model declaring no
// capability whatsoever (every ThinkingSpec/CachingSpec/tool_choice-mode
// getter is nil-safe and returns its zero value), so any explicit
// thinking or tool_choice request against a nil spec always falls back.
func ValidateParams(req *modelv1.GenerationParams, spec *modelv1.ModelSpec) Params {
	if req == nil {
		return Params{}
	}

	resolved, ok := proto.Clone(req).(*modelv1.GenerationParams)
	if !ok {
		// proto.Clone always returns a value of the same concrete type
		// it was given; this branch is unreachable for a well-formed
		// *modelv1.GenerationParams and exists only so the type
		// assertion is checked rather than assumed, per go-style.md's
		// comma-ok rule.
		resolved = &modelv1.GenerationParams{}
	}

	fellBackThinking := false
	thinking := spec.GetThinking()

	if resolved.ThinkingEffort != nil {
		// GetEffort() is nil for a model with no effort ladder, and
		// GetLevels() on a nil control is an empty slice — so a model that
		// declares no effort control fails the membership check and falls
		// back, which is the intended outcome.
		if !slices.Contains(thinking.GetEffort().GetLevels(), resolved.GetThinkingEffort()) {
			resolved.ThinkingEffort = nil
			fellBackThinking = true
		}
	}

	if resolved.ThinkingBudgetTokens != nil {
		if !budgetInRange(resolved.GetThinkingBudgetTokens(), thinking) {
			resolved.ThinkingBudgetTokens = nil
			fellBackThinking = true
		}
	}

	fellBackToolChoice := false
	if resolved.ToolChoice != nil && !toolChoiceSupported(resolved.ToolChoice.GetMode(), spec) {
		resolved.ToolChoice = nil
		fellBackToolChoice = true
	}

	return Params{
		Resolved:           resolved,
		FellBackThinking:   fellBackThinking,
		FellBackToolChoice: fellBackToolChoice,
	}
}

// budgetInRange reports whether budget falls within thinking's declared
// budget control, inclusive of both bounds.
//
// A model declaring no budget control has no meaningful range, so any
// explicit budget request against it is out of range regardless of the
// numeric value — the nil control is checked first rather than relying on
// a nil range's zero bounds, which would coincidentally accept a budget of
// exactly 0.
func budgetInRange(budget int64, thinking *modelv1.ThinkingSpec) bool {
	b := thinking.GetBudget()
	if b == nil {
		return false
	}
	r := b.GetRange()
	if r == nil {
		return false
	}
	return budget >= r.GetMin() && budget <= r.GetMax()
}

// toolChoiceSupported reports whether mode may be forwarded to spec's
// model as-is. TOOL_CHOICE_MODE_AUTO is always supported regardless of
// spec.GetSupportedToolChoiceModes()'s contents — data-types.md defines
// AUTO as "equivalent to omitting tool_choice entirely," so it is always
// the safe fallback target itself, never something a model can fail to
// support. Every other mode (including the invalid zero value,
// TOOL_CHOICE_MODE_UNSPECIFIED, which ToolChoice.mode's doc comment
// states MUST be set) requires exact membership in
// supported_tool_choice_modes.
func toolChoiceSupported(mode modelv1.ToolChoiceMode, spec *modelv1.ModelSpec) bool {
	if mode == modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO {
		return true
	}
	return slices.Contains(spec.GetSupportedToolChoiceModes(), mode)
}
