package hookdispatch

import (
	"fmt"

	"github.com/pluggableharness/agent/internal/telemetry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

// hookPointText maps each dispatchable hook point to the lowercase
// hyphenated name agent.hcl's hook{} label and the trace's
// telemetry.HookPointKey attribute both use. The values are
// internal/telemetry's own constants rather than fresh string literals so
// there is exactly one spelling of "pre-model-call" in the kernel.
//
// context-assemble is deliberately absent: hook-dispatch.md#hook-points
// keeps it on ContextService.Contribute rather than routing it through
// HookSubscriberService, so it is not a point this dispatcher can ever
// serve. pointFromText reports it with its own error message rather than
// letting it fall through as an unrecognized label.
var hookPointText = map[commonv1.HookPoint]string{
	commonv1.HookPoint_HOOK_POINT_SESSION_START:       telemetry.HookPointSessionStart,
	commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL:      telemetry.HookPointPreModelCall,
	commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE: telemetry.HookPointPostModelResponse,
	commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL:       telemetry.HookPointPreToolCall,
	commonv1.HookPoint_HOOK_POINT_PLAN_READY:          telemetry.HookPointPlanReady,
	commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL:      telemetry.HookPointPostToolCall,
	commonv1.HookPoint_HOOK_POINT_POST_APPLY:          telemetry.HookPointPostApply,
	commonv1.HookPoint_HOOK_POINT_SESSION_END:         telemetry.HookPointSessionEnd,
}

// hookTextPoint is hookPointText inverted, built once from it so the two
// can never drift.
var hookTextPoint = func() map[string]commonv1.HookPoint {
	m := make(map[string]commonv1.HookPoint, len(hookPointText))
	for point, text := range hookPointText {
		m[text] = point
	}
	return m
}()

// hookModeText maps each subscription mode to agent.hcl's mode attribute
// vocabulary, sourced from internal/telemetry for the same
// one-spelling reason as hookPointText.
var hookModeText = map[hookv1.HookMode]string{
	hookv1.HookMode_HOOK_MODE_OBSERVE:   telemetry.SubscriberModeObserve,
	hookv1.HookMode_HOOK_MODE_TRANSFORM: telemetry.SubscriberModeTransform,
	hookv1.HookMode_HOOK_MODE_VETO:      telemetry.SubscriberModeVeto,
}

// hookTextMode is hookModeText inverted, built once from it.
var hookTextMode = func() map[string]hookv1.HookMode {
	m := make(map[string]hookv1.HookMode, len(hookModeText))
	for mode, text := range hookModeText {
		m[text] = mode
	}
	return m
}()

// vetoBearingPoints is this kernel's recorded resolution of a gap in
// hook-dispatch.md: the spec repeatedly says "veto-bearing hook point"
// (its dispatch pseudocode's `decision` comment, the veto-mode trust
// model) without ever enumerating which points those are. The resolution
// taken here is the two points that immediately precede a blockable
// action — plan-ready, the terminal gate before a plan applies, and
// pre-tool-call, the terminal gate before a single tool call executes.
// Every other point either fires after the action it describes has
// already happened (post-model-response, post-tool-call, post-apply,
// session-end) or gates nothing a deny could meaningfully stop
// (session-start, pre-model-call).
//
// This is an interpretation, not an invented rule: see this package's
// CLAUDE.md. If the spec later enumerates the set, this map changes with
// it in the same commit.
var vetoBearingPoints = map[commonv1.HookPoint]struct{}{
	commonv1.HookPoint_HOOK_POINT_PLAN_READY:    {},
	commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL: {},
}

// IsVetoBearing reports whether a veto-mode subscription is permitted at
// point, per vetoBearingPoints' recorded resolution above.
func IsVetoBearing(point commonv1.HookPoint) bool {
	_, ok := vetoBearingPoints[point]
	return ok
}

// PointText returns point's lowercase hyphenated name — the agent.hcl
// hook{} label and the telemetry.HookPointKey attribute value. ok is
// false for HOOK_POINT_UNSPECIFIED, for context-assemble (which this
// dispatcher never serves), and for any unrecognized value.
func PointText(point commonv1.HookPoint) (string, bool) {
	text, ok := hookPointText[point]
	return text, ok
}

// ModeText returns mode's agent.hcl vocabulary spelling. ok is false for
// HOOK_MODE_UNSPECIFIED and any unrecognized value.
func ModeText(mode hookv1.HookMode) (string, bool) {
	text, ok := hookModeText[mode]
	return text, ok
}

// pointFromText resolves an agent.hcl hook{} label to its wire enum.
// context-assemble resolves to its own error, distinct from an entirely
// unrecognized label, because it is a real hook point that simply is not
// dispatchable over HookSubscriberService.
func pointFromText(text string) (commonv1.HookPoint, error) {
	if text == telemetry.HookPointContextAssemble {
		return commonv1.HookPoint_HOOK_POINT_UNSPECIFIED, fmt.Errorf(
			"hookdispatch: %w: %q is served by ContextService.Contribute, not HookSubscriberService",
			ErrUnknownPoint, text)
	}
	point, ok := hookTextPoint[text]
	if !ok {
		return commonv1.HookPoint_HOOK_POINT_UNSPECIFIED, fmt.Errorf("hookdispatch: %w: %q", ErrUnknownPoint, text)
	}
	return point, nil
}

// modeFromText resolves an agent.hcl mode attribute to its wire enum.
func modeFromText(text string) (hookv1.HookMode, error) {
	mode, ok := hookTextMode[text]
	if !ok {
		return hookv1.HookMode_HOOK_MODE_UNSPECIFIED, fmt.Errorf("hookdispatch: %w: %q", ErrUnknownMode, text)
	}
	return mode, nil
}
