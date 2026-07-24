package hook

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

// ErrPayloadVariantUnset is returned when a *hookv1.HookPayload has no
// oneof variant set — a wire-contract violation (hook-dispatch.md's
// "payload MUST be set") this package cannot recover a HookPoint from.
var ErrPayloadVariantUnset = errors.New("hook: payload has no oneof variant set")

// pointFromPayload derives the commonv1.HookPoint a wire *hookv1.HookPayload
// was dispatched for from which oneof variant it carries — the wire
// contract makes the set variant *the* point
// (agent-loop/hook-dispatch.md#hook-points), so this is the one place that
// table is encoded.
func pointFromPayload(p *hookv1.HookPayload) (commonv1.HookPoint, error) {
	switch p.GetPayload().(type) {
	case *hookv1.HookPayload_SessionStart:
		return commonv1.HookPoint_HOOK_POINT_SESSION_START, nil
	case *hookv1.HookPayload_PreModelCall:
		return commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL, nil
	case *hookv1.HookPayload_PostModelResponse:
		return commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE, nil
	case *hookv1.HookPayload_PreToolCall:
		return commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL, nil
	case *hookv1.HookPayload_PlanReady:
		return commonv1.HookPoint_HOOK_POINT_PLAN_READY, nil
	case *hookv1.HookPayload_PostToolCall:
		return commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, nil
	case *hookv1.HookPayload_PostApply:
		return commonv1.HookPoint_HOOK_POINT_POST_APPLY, nil
	case *hookv1.HookPayload_SessionEnd:
		return commonv1.HookPoint_HOOK_POINT_SESSION_END, nil
	default:
		return commonv1.HookPoint_HOOK_POINT_UNSPECIFIED, fmt.Errorf("hook: point from payload: %w", ErrPayloadVariantUnset)
	}
}

// payloadToDomain wraps a wire *hookv1.HookPayload as an author-facing
// *Payload, deriving Point from the set oneof variant. subscriptionID
// is req.GetSubscriptionId(), passed through unchanged.
func payloadToDomain(p *hookv1.HookPayload, subscriptionID string) (*Payload, error) {
	point, err := pointFromPayload(p)
	if err != nil {
		return nil, err
	}
	return &Payload{Point: point, SubscriptionID: subscriptionID, proto: p}, nil
}

// cloneHookPayload returns a deep copy of p, or nil if p is nil.
func cloneHookPayload(p *hookv1.HookPayload) *hookv1.HookPayload {
	if p == nil {
		return nil
	}
	cloned, ok := proto.Clone(p).(*hookv1.HookPayload)
	if !ok {
		// proto.Clone always returns a value of the same concrete type
		// it was given; this branch is unreachable for a well-formed
		// *hookv1.HookPayload and exists only so the type assertion is
		// checked rather than blind per go-style.md's comma-ok rule.
		return nil
	}
	return cloned
}

// clearPreModelCallMessages zeroes PreModelCallPayload.messages on p, the
// one field agent-loop/hook-dispatch.md's per-point mutable-field table
// documents as transform-mutable in v1. A no-op for any other variant.
func clearPreModelCallMessages(p *hookv1.HookPayload) {
	if pm := p.GetPreModelCall(); pm != nil {
		pm.Messages = nil
	}
}

// payloadsEqualExceptMutable reports whether resp is identical to req once
// point's transform-mutable fields
// (agent-loop/hook-dispatch.md#per-point-transform-mutable-fields) are
// cleared from both sides first. Only pre-model-call's messages field is
// mutable in v1 — every other point takes the default branch, clearing
// nothing, which makes the check equivalent to a plain proto.Equal and so
// also catches a transform response returning the wrong oneof variant
// entirely (proto.Equal treats a differing oneof case as unequal).
func payloadsEqualExceptMutable(point commonv1.HookPoint, req, resp *hookv1.HookPayload) bool {
	reqClone := cloneHookPayload(req)
	respClone := cloneHookPayload(resp)
	if point == commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL {
		clearPreModelCallMessages(reqClone)
		clearPreModelCallMessages(respClone)
	}
	return proto.Equal(reqClone, respClone)
}
