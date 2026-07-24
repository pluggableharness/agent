package hook

import (
	"context"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

// Mode is a subscription's operator-declared dispatch mode
// (agent.hcl's hook{} block), echoed on every DispatchHook request so a
// subscriber knows which of the three outcome shapes is expected back.
// Mode is a direct alias of the generated enum — per go-layout.md's
// "exactly one Go representation of each wire message", this package does
// not define a parallel enum type, only the idiomatic, non-stuttering
// constant names below.
type Mode = hookv1.HookMode

// The three dispatch modes a Subscriber facet may be invoked under, per
// agent-loop/hook-dispatch.md#dispatch-modes--response-shapes.
const (
	// ModeObserve is read-only and fire-and-forget: an Observer's error
	// is logged by the kernel and never aborts the chain.
	ModeObserve = hookv1.HookMode_HOOK_MODE_OBSERVE
	// ModeTransform is a sequential chain member: a Transformer receives
	// the prior stage's payload and returns a modified version of the
	// same variant.
	ModeTransform = hookv1.HookMode_HOOK_MODE_TRANSFORM
	// ModeVeto expects an explicit allow/deny verdict from a Vetoer.
	ModeVeto = hookv1.HookMode_HOOK_MODE_VETO
)

// Decision is a Vetoer's allow/deny verdict over a whole Payload — a
// direct alias of the generated enum, per the same "one wire
// representation" rule Mode follows above.
type Decision = hookv1.HookDecision

// The two valid Decision values a Vetoer may return.
// DecisionUnspecified is exported only so tests and validation code have
// a name for the invalid zero value; a Vetoer implementation MUST NOT
// return it (server.go rejects it as HOOK_ERROR_CATEGORY_INVALID_RESPONSE
// before it reaches the wire).
const (
	DecisionUnspecified = hookv1.HookDecision_HOOK_DECISION_UNSPECIFIED
	DecisionAllow       = hookv1.HookDecision_HOOK_DECISION_ALLOW
	DecisionDeny        = hookv1.HookDecision_HOOK_DECISION_DENY
)

// Points is the ordered set of the eight hook points this package's
// HookSubscriberService surface dispatches
// (agent-loop/hook-dispatch.md#hook-points). context-assemble —
// architecture.md's ninth named point — is deliberately absent; it stays
// on ContextService.Contribute (doc.go).
var Points = []commonv1.HookPoint{
	commonv1.HookPoint_HOOK_POINT_SESSION_START,
	commonv1.HookPoint_HOOK_POINT_PRE_MODEL_CALL,
	commonv1.HookPoint_HOOK_POINT_POST_MODEL_RESPONSE,
	commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL,
	commonv1.HookPoint_HOOK_POINT_PLAN_READY,
	commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
	commonv1.HookPoint_HOOK_POINT_POST_APPLY,
	commonv1.HookPoint_HOOK_POINT_SESSION_END,
}

// Payload is the author-facing wrapper for one hook-point firing's
// payload. Point is derived from which oneof variant the wire message
// carries — the wire contract makes the set variant *the* hook point
// (hook-dispatch.md#hook-points), so a Subscriber facet does not have to
// re-discover it via its own type switch on every call. SubscriptionID
// disambiguates a plugin declaring more than one hook{} block at the same
// HookPoint (empty when the plugin has exactly one subscription there).
//
// This type does not duplicate the eight per-point payload messages
// (SessionStartPayload, PreModelCallPayload, ...) as a second, parallel Go
// representation — go-layout.md's "exactly one Go representation of each
// wire message" rule. Use Proto to reach the generated oneof carrier and
// its own GetXxx accessors (e.g. payload.Proto().GetPreModelCall()).
type Payload struct {
	// Point is the hook point this payload was dispatched for.
	Point commonv1.HookPoint
	// SubscriptionID disambiguates multiple hook{} blocks at the same
	// Point declared by the same plugin. Empty when there is exactly one.
	SubscriptionID string

	proto *hookv1.HookPayload
}

// Proto returns the underlying generated *hookv1.HookPayload oneof
// carrier this Payload wraps.
func (p *Payload) Proto() *hookv1.HookPayload {
	if p == nil {
		return nil
	}
	return p.proto
}

// NewPayload wraps proto as a Payload, deriving Point from
// whichever oneof variant proto carries. Returns ErrPayloadVariantUnset if
// none is set.
//
// A Transformer has two equally valid ways to produce its return value:
// mutate the payload it was handed in place via its Proto accessor (e.g.
// `payload.Proto().GetPreModelCall().Messages = redacted`) and return the
// same *Payload, or build a fresh *hookv1.HookPayload and wrap it with
// NewPayload before returning it. server.go's DispatchHook snapshots
// the request payload before invoking Transform specifically so both
// styles are validated against the same pristine baseline — an in-place
// mutation is never compared against itself.
func NewPayload(proto *hookv1.HookPayload) (*Payload, error) {
	return payloadToDomain(proto, "")
}

// Observer handles a HOOK_MODE_OBSERVE dispatch. Observe is read-only and
// fire-and-forget: per agent-loop/hook-dispatch.md#subscriber-error-handling,
// an Observer error is logged by the kernel (as an event on the state
// backend, producer = this subscriber) and the kernel's dispatch chain
// continues regardless — the failure is never fatal to that chain. See
// server.go's DispatchHook for exactly what this SDK layer does with an
// Observe error, since the chain itself is kernel-side, not here.
type Observer interface {
	Observe(ctx context.Context, payload *Payload) error
}

// Transformer handles a HOOK_MODE_TRANSFORM dispatch. It MUST return a
// payload of the same Payload oneof variant it received, and MUST NOT
// change any field this hook point's mutable-field table
// (agent-loop/hook-dispatch.md#per-point-transform-mutable-fields) doesn't
// list as transform-mutable — in v1 that is exactly one field,
// pre-model-call's messages. server.go validates both constraints before
// a Transform response ever reaches the wire; a violation there is
// reported as HOOK_ERROR_CATEGORY_INVALID_RESPONSE, never silently
// forwarded.
type Transformer interface {
	Transform(ctx context.Context, payload *Payload) (*Payload, error)
}

// Vetoer handles a HOOK_MODE_VETO dispatch. It MUST return DecisionAllow
// or DecisionDeny — never DecisionUnspecified, which server.go rejects at
// the SDK layer before it ever reaches the wire (as
// HOOK_ERROR_CATEGORY_INVALID_RESPONSE) rather than letting an
// under-specified verdict through. A returned error is treated identically
// to an explicit deny by the kernel — fail-closed
// (agent-loop/hook-dispatch.md#timeout-behavior) — but this SDK layer does
// not itself substitute DecisionDeny for an error; see errors.go and
// Service.DispatchHook's doc comment (server.go) for why the fail-closed
// conversion happens at the gRPC-status level, in the kernel, not by this
// layer fabricating an in-band VetoResult.
type Vetoer interface {
	Veto(ctx context.Context, payload *Payload) (Decision, error)
}
