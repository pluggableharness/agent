package hookpayload

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

// ErrInvalidResponse is the sentinel every shape/mutation violation
// wraps, per hook-dispatch.md#invalid_response-handling.
var ErrInvalidResponse = errors.New("hookpayload: invalid response")

// Point returns the commonv1.HookPoint the payload's set oneof variant
// corresponds to, per hook-dispatch.md#hook-points' table (session-start
// -> HOOK_POINT_SESSION_START, pre-model-call -> HOOK_POINT_PRE_MODEL_CALL,
// etc.). ok is false if no variant is set (a zero-value/malformed payload).
func Point(p *hookv1.HookPayload) (commonv1.HookPoint, bool) {
	if p == nil {
		return commonv1.HookPoint_HOOK_POINT_UNSPECIFIED, false
	}

	switch p.Payload.(type) {
	case *hookv1.HookPayload_SessionStart:
		return commonv1.HookPoint_HOOK_POINT_SESSION_START, true
	case *hookv1.HookPayload_PreModelCall:
		return commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL, true
	case *hookv1.HookPayload_PostModelResponse:
		return commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE, true
	case *hookv1.HookPayload_PreToolCall:
		return commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL, true
	case *hookv1.HookPayload_PlanReady:
		return commonv1.HookPoint_HOOK_POINT_PLAN_READY, true
	case *hookv1.HookPayload_PostToolCall:
		return commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, true
	case *hookv1.HookPayload_PostApply:
		return commonv1.HookPoint_HOOK_POINT_POST_APPLY, true
	case *hookv1.HookPayload_SessionEnd:
		return commonv1.HookPoint_HOOK_POINT_SESSION_END, true
	default:
		return commonv1.HookPoint_HOOK_POINT_UNSPECIFIED, false
	}
}

// Mutable returns the transform-mutable field names for point, per
// hook-dispatch.md#per-point-transform-mutable-fields. Only
// HOOK_POINT_PRE_MODEL_CALL returns a non-empty slice ({"messages"});
// every other point returns nil/empty.
func Mutable(point commonv1.HookPoint) []string {
	switch point {
	case commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL:
		return []string{"messages"}
	default:
		return nil
	}
}

// ApplyTransform validates a transform subscriber's response against
// req and returns the accepted merged payload. It clones req, copies only
// the fields Mutable(point) names from resp onto the clone, then compares
// the clone against resp — any inequality on an immutable field wraps
// ErrInvalidResponse. At a non-mutable point, resp MUST be
// proto.Equal to req, or this is a violation.
func ApplyTransform(req *hookv1.HookPayload, resp *hookv1.HookPayload) (*hookv1.HookPayload, error) {
	if req == nil || resp == nil {
		return nil, fmt.Errorf("%w: nil payload", ErrInvalidResponse)
	}

	// Get the point from the request.
	point, ok := Point(req)
	if !ok {
		return nil, fmt.Errorf("%w: request payload has no variant set", ErrInvalidResponse)
	}

	// Get the point from the response.
	respPoint, ok := Point(resp)
	if !ok {
		return nil, fmt.Errorf("%w: response payload has no variant set", ErrInvalidResponse)
	}

	// The variants must match.
	if point != respPoint {
		return nil, fmt.Errorf("%w: response variant does not match request variant", ErrInvalidResponse)
	}

	// Get mutable fields for this point.
	mutableFields := Mutable(point)

	if len(mutableFields) == 0 {
		// At a non-mutable point, the response must be byte-identical to the request.
		if !proto.Equal(req, resp) {
			return nil, fmt.Errorf("%w: response differs from request at immutable point", ErrInvalidResponse)
		}
		cloned := proto.Clone(req)
		if cloned == nil {
			return nil, fmt.Errorf("%w: failed to clone request payload", ErrInvalidResponse)
		}
		return cloned.(*hookv1.HookPayload), nil
	}

	// At a mutable point, we need to check that only mutable fields differ.
	// Clone the request and copy the mutable fields from the response.
	cloned := proto.Clone(req)
	if cloned == nil {
		return nil, fmt.Errorf("%w: failed to clone request payload", ErrInvalidResponse)
	}
	merged := cloned.(*hookv1.HookPayload)

	// For pre-model-call, copy the messages field.
	if point == commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL {
		respPayload := resp.GetPreModelCall()
		if respPayload == nil {
			return nil, fmt.Errorf("%w: response pre-model-call payload is nil", ErrInvalidResponse)
		}

		reqPayload := req.GetPreModelCall()
		if reqPayload == nil {
			return nil, fmt.Errorf("%w: request pre-model-call payload is nil", ErrInvalidResponse)
		}

		// Copy only the messages field.
		merged.Payload = &hookv1.HookPayload_PreModelCall{
			PreModelCall: &hookv1.PreModelCallPayload{
				Messages: respPayload.Messages,
				Model:    reqPayload.Model, // Keep original model (immutable)
			},
		}

		// Now verify that only the messages field changed.
		// The model field must be identical.
		if !proto.Equal(reqPayload.Model, respPayload.Model) {
			return nil, fmt.Errorf("%w: response mutated immutable field 'model'", ErrInvalidResponse)
		}
	}

	return merged, nil
}

// ValidateShape checks that resp's oneof variant matches the mode the
// request declared (HOOK_MODE_OBSERVE -> ObserveAck, HOOK_MODE_TRANSFORM
// -> TransformResult, HOOK_MODE_VETO -> VetoResult), and that a veto
// response's decision is not HOOK_DECISION_UNSPECIFIED. Wraps
// ErrInvalidResponse on any mismatch.
func ValidateShape(mode hookv1.HookMode, resp *hookv1.DispatchHookResponse) error {
	if resp == nil {
		return fmt.Errorf("%w: response is nil", ErrInvalidResponse)
	}

	switch mode {
	case hookv1.HookMode_HOOK_MODE_OBSERVE:
		if _, ok := resp.Outcome.(*hookv1.DispatchHookResponse_Observe); !ok {
			return fmt.Errorf("%w: observe mode requires ObserveAck outcome", ErrInvalidResponse)
		}
		return nil

	case hookv1.HookMode_HOOK_MODE_TRANSFORM:
		if _, ok := resp.Outcome.(*hookv1.DispatchHookResponse_Transform); !ok {
			return fmt.Errorf("%w: transform mode requires TransformResult outcome", ErrInvalidResponse)
		}
		return nil

	case hookv1.HookMode_HOOK_MODE_VETO:
		vetoResult, ok := resp.Outcome.(*hookv1.DispatchHookResponse_Veto)
		if !ok {
			return fmt.Errorf("%w: veto mode requires VetoResult outcome", ErrInvalidResponse)
		}
		if vetoResult.Veto == nil {
			return fmt.Errorf("%w: veto result is nil", ErrInvalidResponse)
		}
		if vetoResult.Veto.Decision == hookv1.HookDecision_HOOK_DECISION_UNSPECIFIED {
			return fmt.Errorf("%w: veto decision must not be HOOK_DECISION_UNSPECIFIED", ErrInvalidResponse)
		}
		return nil

	default:
		return fmt.Errorf("%w: invalid or unspecified mode", ErrInvalidResponse)
	}
}

// Category maps a dispatch failure to the wire HookErrorCategory a
// caller persists on a hook_error event, mode-appropriately: an invalid-
// shape error at any mode is HOOK_ERROR_CATEGORY_INVALID_RESPONSE; other
// errors map to whatever category constants docs/specifications/agent-loop/
// hook-dispatch.md#invalid_response-handling and the generated
// HookErrorCategory enum actually define.
func Category(mode hookv1.HookMode, err error) hookv1.HookErrorCategory {
	if errors.Is(err, ErrInvalidResponse) {
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE
	}

	// For other errors, map based on mode and error type.
	switch mode {
	case hookv1.HookMode_HOOK_MODE_OBSERVE:
		// Observe errors don't abort the chain, but if we're asked to categorize,
		// it's likely an unexpected error. Use UNKNOWN as a catch-all.
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN

	case hookv1.HookMode_HOOK_MODE_TRANSFORM:
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TRANSFORM_FAILED

	case hookv1.HookMode_HOOK_MODE_VETO:
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_VETO_FAILED

	default:
		return hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN
	}
}
