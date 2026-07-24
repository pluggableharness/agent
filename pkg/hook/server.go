package hook

import (
	"context"

	"google.golang.org/grpc"

	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// Service adapts an author-supplied subscriber value (implementing one or
// more of Observer, Transformer, Vetoer) to
// hookv1.HookSubscriberServiceServer, and to plugin.Service so it can be
// added to plugin.Config.Services alongside a plugin's primary category
// service (doc.go). Construct one with NewService.
//
// HookSubscriberService has no Describe RPC — unlike a category service, a
// hook subscriber advertises nothing about itself beyond what agent.hcl's
// hook{} block already declares — so, unlike the six category SDKs' own
// service adapters, Service needs no plugin.Identity at construction.
type Service struct {
	hookv1.UnimplementedHookSubscriberServiceServer

	observer    Observer
	transformer Transformer
	vetoer      Vetoer
}

var (
	_ plugin.Service                     = (*Service)(nil)
	_ hookv1.HookSubscriberServiceServer = (*Service)(nil)
)

// NewService builds a Service wrapping subscriber. subscriber is type-
// asserted against Observer, Transformer, and Vetoer independently
// (doc.go's "optional-facet" design) — implementing none of the three is
// accepted at construction time (every DispatchHook call will then fail
// with errNotImplemented, a legitimate outcome for a Service that is only
// ever muxed onto Config.Services by mistake) rather than treated as a
// construction error, since NewService itself cannot otherwise fail.
func NewService(subscriber any) *Service {
	s := &Service{}
	if o, ok := subscriber.(Observer); ok {
		s.observer = o
	}
	if t, ok := subscriber.(Transformer); ok {
		s.transformer = t
	}
	if v, ok := subscriber.(Vetoer); ok {
		s.vetoer = v
	}
	return s
}

// Register registers this Service's HookSubscriberService handler on gs,
// satisfying plugin.Service.
func (s *Service) Register(gs *grpc.Server) {
	hookv1.RegisterHookSubscriberServiceServer(gs, s)
}

// DispatchHook implements hookv1.HookSubscriberServiceServer, delivering
// one hook-point firing to the wrapped subscriber's matching facet and
// validating its response against the mode-specific shape rules
// agent-loop/hook-dispatch.md defines before anything reaches the wire.
//
// # Where the dispatch chain boundary sits
//
// This method adapts exactly one subscriber's exactly one RPC invocation.
// The ordered multi-subscriber chain per hook point — declaration order,
// interleaved observe/transform/veto, short-circuiting on a non-allow veto
// — is entirely kernel-side
// (agent-loop/hook-dispatch.md#dispatch-order-and-payload-flow); this
// package has no visibility into other subscribers and does not try to
// simulate any part of that loop.
//
// # What "observe errors are never fatal to the chain" means here
//
// Concretely: this method does not swallow an Observer error into a
// fabricated success. If s.observer.Observe returns an error, DispatchHook
// returns a normal gRPC error (errObserveFailed) — never a synthesized
// ObserveAck — because it is the kernel's dispatch loop, on receiving that
// error for an observe-mode call, that logs it (as an event on the state
// backend, producer = this subscriber) and moves on to the next subscriber
// without aborting or altering the payload
// (agent-loop/hook-dispatch.md#subscriber-error-handling). Reporting the
// error honestly is what lets the kernel build that audit trail; silently
// answering ObserveAck instead would hide a real failure from it. The
// "never fatal" guarantee itself is enforced one layer up, in the kernel's
// per-mode switch, not by anything in this method.
//
// # Fail-closed veto and the gRPC-status boundary
//
// A Vetoer returning DecisionUnspecified, an error, or exceeding its
// context deadline all produce a non-nil gRPC error from this method,
// never an in-band VetoResult{Decision: DecisionDeny}. rpc_response.pb.go's
// VetoResult doc comment is explicit that "the fail-closed behavior for a
// genuinely absent/erroring response is handled at the gRPC-status level,
// not by this enum's zero value" — so converting any veto-mode error into
// HOOK_DECISION_DENY is the kernel dispatch loop's job (per its
// pseudocode: "if outcome is error or timeout: decision := deny"), not
// this SDK layer's. This method's only obligation is to make sure a
// malformed or failing veto response is never mistaken for
// HOOK_DECISION_ALLOW.
func (s *Service) DispatchHook(ctx context.Context, req *hookv1.DispatchHookRequest) (*hookv1.DispatchHookResponse, error) {
	if cerr := mapContextErr(ctx.Err()); cerr != nil {
		return nil, cerr
	}

	reqProto := req.GetPayload()
	if reqProto == nil {
		return nil, errInvalidRequest("payload_unset", "hook: dispatch: request payload is unset")
	}
	payload, err := payloadToDomain(reqProto, req.GetSubscriptionId())
	if err != nil {
		return nil, errInvalidRequest("payload_variant_unset", "hook: dispatch: "+err.Error())
	}

	switch req.GetMode() {
	case hookv1.HookMode_HOOK_MODE_OBSERVE:
		return s.dispatchObserve(ctx, payload)
	case hookv1.HookMode_HOOK_MODE_TRANSFORM:
		return s.dispatchTransform(ctx, payload, reqProto)
	case hookv1.HookMode_HOOK_MODE_VETO:
		return s.dispatchVeto(ctx, payload)
	default:
		return nil, errInvalidRequest("mode_unset", "hook: dispatch: request mode is unspecified")
	}
}

// dispatchObserve invokes s.observer and, on success, returns the empty
// ObserveAck outcome (rpc_response.pb.go's DispatchHookResponse_ObserveAck)
// the kernel discards unconditionally.
func (s *Service) dispatchObserve(ctx context.Context, payload *Payload) (*hookv1.DispatchHookResponse, error) {
	if s.observer == nil {
		return nil, errNotImplemented(payload.Point, ModeObserve)
	}
	if err := s.observer.Observe(ctx, payload); err != nil {
		if cerr := mapContextErr(err); cerr != nil {
			return nil, cerr
		}
		return nil, errObserveFailed(payload.Point, err)
	}
	return &hookv1.DispatchHookResponse{
		Outcome: &hookv1.DispatchHookResponse_Observe{
			Observe: &hookv1.DispatchHookResponse_ObserveAck{},
		},
	}, nil
}

// dispatchTransform invokes s.transformer and validates its returned
// payload against a pristine snapshot of the request taken before the
// call, before returning a TransformResult outcome: same oneof variant,
// and no field changed outside the current point's transform-mutable set
// (agent-loop/hook-dispatch.md#per-point-transform-mutable-fields).
//
// The snapshot is taken before s.transformer.Transform runs, and payload
// itself (not a defensive copy of it) is what's passed to Transform — per
// NewPayload's doc comment, a Transformer is allowed to mutate
// payload.Proto() in place and return the same *Payload. Comparing
// against a live reqProto instead of this snapshot would make that style
// compare a mutated value against itself and let any mutation through,
// defeating the whole check.
func (s *Service) dispatchTransform(ctx context.Context, payload *Payload, reqProto *hookv1.HookPayload) (*hookv1.DispatchHookResponse, error) {
	if s.transformer == nil {
		return nil, errNotImplemented(payload.Point, ModeTransform)
	}
	reqSnapshot := cloneHookPayload(reqProto)
	result, err := s.transformer.Transform(ctx, payload)
	if err != nil {
		if cerr := mapContextErr(err); cerr != nil {
			return nil, cerr
		}
		return nil, errTransformFailed(payload.Point, err)
	}
	respProto := result.Proto()
	if respProto == nil {
		return nil, errInvalidResponse(payload.Point, ModeTransform, "transform_payload_unset", "hook: transform: response payload is unset")
	}
	if !payloadsEqualExceptMutable(payload.Point, reqSnapshot, respProto) {
		return nil, errInvalidResponse(payload.Point, ModeTransform, "transform_payload_mutated",
			"hook: transform: response changed the oneof variant or a field this hook point does not document as transform-mutable")
	}
	return &hookv1.DispatchHookResponse{
		Outcome: &hookv1.DispatchHookResponse_Transform{
			Transform: &hookv1.DispatchHookResponse_TransformResult{Payload: respProto},
		},
	}, nil
}

// dispatchVeto invokes s.vetoer and validates its returned Decision is
// DecisionAllow or DecisionDeny before returning a VetoResult outcome.
// DecisionUnspecified is rejected as HOOK_ERROR_CATEGORY_INVALID_RESPONSE
// — see DispatchHook's doc comment for why this method never substitutes
// DecisionDeny itself.
func (s *Service) dispatchVeto(ctx context.Context, payload *Payload) (*hookv1.DispatchHookResponse, error) {
	if s.vetoer == nil {
		return nil, errNotImplemented(payload.Point, ModeVeto)
	}
	decision, err := s.vetoer.Veto(ctx, payload)
	if err != nil {
		if cerr := mapContextErr(err); cerr != nil {
			return nil, cerr
		}
		return nil, errVetoFailed(payload.Point, err)
	}
	if decision != DecisionAllow && decision != DecisionDeny {
		return nil, errInvalidResponse(payload.Point, ModeVeto, "veto_decision_unspecified", "hook: veto: response decision is unspecified")
	}
	return &hookv1.DispatchHookResponse{
		Outcome: &hookv1.DispatchHookResponse_Veto{
			Veto: &hookv1.DispatchHookResponse_VetoResult{Decision: decision},
		},
	}, nil
}
