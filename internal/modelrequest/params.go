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
//   - Resolved.thinking_effort is cleared unless spec's ThinkingSpec.mode
//     is THINKING_MODE_DISCRETE_EFFORT and the requested value appears in
//     ThinkingSpec.effort_levels.
//   - Resolved.thinking_budget_tokens is cleared unless spec's
//     ThinkingSpec.mode is THINKING_MODE_CONTINUOUS_BUDGET and the
//     requested value falls within ThinkingSpec.budget_range (inclusive of
//     both bounds).
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
		if thinking.GetMode() != modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT ||
			!slices.Contains(thinking.GetEffortLevels(), resolved.GetThinkingEffort()) {
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

// budgetInRange reports whether budget falls within thinking's
// budget_range, inclusive of both bounds, and thinking's mode actually
// governs a token budget at all. A thinking with mode !=
// THINKING_MODE_CONTINUOUS_BUDGET has no meaningful budget_range per
// data-types.md's ThinkingSpec doc ("required if mode ==
// continuous_budget"), so any explicit budget request against such a
// model is out of range regardless of the numeric value.
func budgetInRange(budget int64, thinking *modelv1.ThinkingSpec) bool {
	if thinking.GetMode() != modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET {
		return false
	}
	r := thinking.GetBudgetRange()
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
