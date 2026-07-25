package kernelcallback

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// kernelOwnedEventKinds are the EventKinds a plugin-facing Emit call MUST
// reject outright, before any other validation. state-backend.md's
// conformance table requires cost_ledger/plan_items populated in the SAME
// transaction as the message/plan event that produced them
// (statebackend.Session.AppendMessage/AppendPlan enforce this at the
// sqlite level) — a generic Emit(kind, payload) call has no way to also
// supply a CostEntry or []PlanItem, so only sessionstate.Live's own
// EmitMessage/EmitPlan (called from a future kernel-internal path, never
// this RPC — see internal/sessionstate/CLAUDE.md) can produce these kinds
// correctly.
var kernelOwnedEventKinds = map[kernelv1.EventKind]bool{
	kernelv1.EventKind_EVENT_KIND_MESSAGE: true,
	kernelv1.EventKind_EVENT_KIND_PLAN:    true,
}

// Emit implements the Emit RPC (kernel-callbacks.md's Emit): authorizes
// req.SessionId via authorizedSession, rejects the two kernel-owned kinds
// (see kernelOwnedEventKinds above), validates the remaining fields, and
// persists the event via the authorized session's *sessionstate.Live.
func (s *Server) Emit(ctx context.Context, req *kernelv1.EmitRequest) (*kernelv1.EmitResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackEmit(ctx, req.GetSessionId(), s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: emit", "session_id", req.GetSessionId(), "kind", req.GetKind())

	live, err := s.authorizedSession(ctx, req.GetSessionId())
	if err != nil {
		s.logger.WarnContext(ctx, "kernelcallback: emit: rejected", "err", err)
		return nil, err
	}

	if kernelOwnedEventKinds[req.GetKind()] {
		err = status.Errorf(codes.PermissionDenied, "kernelcallback: emit: %s is kernel-owned and cannot be emitted by a plugin", req.GetKind())
		s.logger.WarnContext(ctx, "kernelcallback: emit: rejected", "err", err)
		return nil, err
	}
	if req.GetKind() == kernelv1.EventKind_EVENT_KIND_UNSPECIFIED {
		err = status.Error(codes.InvalidArgument, "kernelcallback: emit: kind is required")
		s.logger.WarnContext(ctx, "kernelcallback: emit: rejected", "err", err)
		return nil, err
	}
	if req.GetSchemaVersion() == "" {
		err = status.Error(codes.InvalidArgument, "kernelcallback: emit: schema_version is required")
		s.logger.WarnContext(ctx, "kernelcallback: emit: rejected", "err", err)
		return nil, err
	}
	if req.GetPayload() == nil {
		err = status.Error(codes.InvalidArgument, "kernelcallback: emit: payload is required")
		s.logger.WarnContext(ctx, "kernelcallback: emit: rejected", "err", err)
		return nil, err
	}

	outcome, emitErr := live.Emit(ctx, sessionstate.EmitRecord{
		Producer:      s.producer,
		Kind:          req.GetKind(),
		SchemaVersion: req.GetSchemaVersion(),
		Payload:       req.GetPayload(),
	})
	if emitErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: emit: %v", emitErr)
		s.logger.ErrorContext(ctx, "kernelcallback: emit: failed", "err", emitErr)
		return nil, err
	}

	return &kernelv1.EmitResult{Id: outcome.ID, Sequence: outcome.Sequence}, nil
}
