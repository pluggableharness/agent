package kernelcallback

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// ReadEvents implements the ReadEvents RPC (kernel-callbacks.md's
// ReadEvents): authorizes req.SessionId via authorizedSession, then
// streams the authorized session's persisted events matching req's
// filters, sequence-ascending (determinism.md — never by time), via the
// live session's sessionstate.Live.Events read pass-through.
func (s *Server) ReadEvents(req *kernelv1.ReadEventsRequest, stream kernelv1.KernelCallbackService_ReadEventsServer) error {
	ctx := stream.Context()
	ctx, span := s.telemetry.StartKernelCallbackReadEvents(ctx, req.GetSessionId(), s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: read_events", "session_id", req.GetSessionId())

	live, err := s.authorizedSession(ctx, req.GetSessionId())
	if err != nil {
		s.logger.WarnContext(ctx, "kernelcallback: read_events: rejected", "err", err)
		return err
	}

	q := statebackend.EventQuery{
		Kinds:        req.GetKinds(),
		FromSequence: req.FromSequence,
		Limit:        req.Limit,
	}

	for ev, evErr := range live.Events(ctx, q) {
		if evErr != nil {
			err = status.Errorf(codes.Internal, "kernelcallback: read_events: %v", evErr)
			s.logger.ErrorContext(ctx, "kernelcallback: read_events: failed", "err", evErr)
			return err
		}
		stored := &kernelv1.StoredEvent{
			Sequence:      ev.Sequence,
			Id:            ev.ID,
			Time:          timestamppb.New(ev.Timestamp),
			Kind:          ev.Kind,
			Producer:      ev.Producer,
			SchemaVersion: ev.SchemaVersion,
			Payload:       ev.Payload,
		}
		if sendErr := stream.Send(stored); sendErr != nil {
			err = sendErr
			return err
		}
	}
	return nil
}
