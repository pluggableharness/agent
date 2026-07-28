package kernelcallback

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/metadata"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metadatav1 "github.com/pluggableharness/agent/pkg/metadata/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// GetSessionState implements the GetSessionState RPC: the fixed-schema
// "where am I" snapshot for one authorized session.
func (s *Server) GetSessionState(ctx context.Context, req *kernelv1.GetSessionStateRequest) (*kernelv1.GetSessionStateResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackGetSession(ctx, req.GetSessionId(), s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: get_session_state", "session_id", req.GetSessionId())

	if _, err = s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		s.logger.WarnContext(ctx, "kernelcallback: get_session_state: rejected", "err", err)
		return nil, err
	}

	if s.host() != nil {
		state, hostErr := s.host().GetSessionState(ctx, req.GetSessionId())
		if hostErr == nil && state != nil {
			return &kernelv1.GetSessionStateResult{State: state}, nil
		}
		// Fall through to live-table assembly when host has no handle.
	}

	live, err := s.authorizedSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	meta, metaErr := live.Meta(ctx)
	if metaErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: get_session_state: %v", metaErr)
		return nil, err
	}
	totalCostUSD, costErr := live.TotalCostUSD(ctx)
	if costErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: get_session_state: cost: %v", costErr)
		return nil, err
	}

	info := &sessionv1.SessionInfo{
		SessionId: meta.SessionID,
		Profile:   meta.Profile,
		Status:    meta.Status,
		Depth:     int32(meta.Depth), // #nosec G115 -- session-tree depth is a tiny bounded count
		StartedAt: timestamppb.New(meta.StartedAt),
	}
	if meta.ParentSessionID != "" {
		info.ParentSessionId = &meta.ParentSessionID
	}
	if meta.EndedAt != nil {
		info.EndedAt = timestamppb.New(*meta.EndedAt)
	}
	if totalCostUSD != 0 {
		info.CostUsd = &totalCostUSD
	}

	elapsed := time.Since(meta.StartedAt)
	if meta.EndedAt != nil {
		elapsed = meta.EndedAt.Sub(meta.StartedAt)
	}

	state := &sessionv1.SessionState{
		Info:    info,
		Elapsed: durationpb.New(elapsed),
	}
	return &kernelv1.GetSessionStateResult{State: state}, nil
}

// PublishMetadata upserts a MetadataBlock, stamps producer/liveness, and
// republishes on topic kernel.metadata.
func (s *Server) PublishMetadata(ctx context.Context, req *kernelv1.PublishMetadataRequest) (*kernelv1.PublishMetadataResult, error) {
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if s.metadata == nil {
		return nil, status.Error(codes.FailedPrecondition, "kernelcallback: metadata store not configured")
	}
	stored, err := s.metadata.Publish(req.GetSessionId(), s.producer, req.GetBlock())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "kernelcallback: publish metadata: %v", err)
	}
	s.publishMetadataBus(ctx, stored)
	return &kernelv1.PublishMetadataResult{Block: stored}, nil
}

// RetractMetadata flips a block to DISCONNECTED and republishes it.
func (s *Server) RetractMetadata(ctx context.Context, req *kernelv1.RetractMetadataRequest) (*kernelv1.RetractMetadataResult, error) {
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if s.metadata == nil {
		return nil, status.Error(codes.FailedPrecondition, "kernelcallback: metadata store not configured")
	}
	stored, err := s.metadata.Retract(req.GetSessionId(), req.GetBlockId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "kernelcallback: retract metadata: %v", err)
	}
	s.publishMetadataBus(ctx, stored)
	return &kernelv1.RetractMetadataResult{Block: stored}, nil
}

// ListMetadata returns every known MetadataBlock for an authorized session.
func (s *Server) ListMetadata(ctx context.Context, req *kernelv1.ListMetadataRequest) (*kernelv1.ListMetadataResult, error) {
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if s.metadata == nil {
		return &kernelv1.ListMetadataResult{}, nil
	}
	return &kernelv1.ListMetadataResult{Blocks: s.metadata.List(req.GetSessionId())}, nil
}

func (s *Server) publishMetadataBus(ctx context.Context, block *metadatav1.MetadataBlock) {
	if s.bus == nil || block == nil {
		return
	}
	payload, err := proto.Marshal(block)
	if err != nil {
		s.logger.WarnContext(ctx, "kernelcallback: metadata bus marshal failed", "err", err)
		return
	}
	if err := s.bus.Publish(ctx, eventbus.Event{Topic: metadata.Topic, Payload: payload}); err != nil {
		s.logger.WarnContext(ctx, "kernelcallback: metadata bus publish failed", "err", err)
	}
}

// SubmitInput submits operator content as the next turn.
func (s *Server) SubmitInput(ctx context.Context, req *kernelv1.SubmitInputRequest) (*kernelv1.SubmitInputResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: SubmitInput not implemented")
	}
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	turnID, err := s.host().SubmitInput(ctx, req.GetSessionId(), req.GetContent())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kernelcallback: submit input: %v", err)
	}
	return &kernelv1.SubmitInputResult{TurnId: turnID}, nil
}

// ResolvePlanDecision answers a pending plan item.
func (s *Server) ResolvePlanDecision(ctx context.Context, req *kernelv1.ResolvePlanDecisionRequest) (*kernelv1.ResolvePlanDecisionResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: ResolvePlanDecision not implemented")
	}
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if err := s.host().ResolvePlanDecision(ctx, req); err != nil {
		return nil, mapHostErr(err)
	}
	return &kernelv1.ResolvePlanDecisionResult{}, nil
}

// ResolveInteractive answers a pending interactive-kind tool call.
func (s *Server) ResolveInteractive(ctx context.Context, req *kernelv1.ResolveInteractiveRequest) (*kernelv1.ResolveInteractiveResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: ResolveInteractive not implemented")
	}
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if err := s.host().ResolveInteractive(ctx, req); err != nil {
		return nil, mapHostErr(err)
	}
	return &kernelv1.ResolveInteractiveResult{}, nil
}

// Interrupt cancels the running turn for a session.
func (s *Server) Interrupt(ctx context.Context, req *kernelv1.InterruptRequest) (*kernelv1.InterruptResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: Interrupt not implemented")
	}
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if err := s.host().Interrupt(ctx, req.GetSessionId()); err != nil {
		return nil, status.Errorf(codes.Internal, "kernelcallback: interrupt: %v", err)
	}
	return &kernelv1.InterruptResult{}, nil
}

// CreateSession creates a new session and auto-attaches the caller.
func (s *Server) CreateSession(ctx context.Context, req *kernelv1.CreateSessionRequest) (*kernelv1.CreateSessionResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: CreateSession not implemented")
	}
	info, err := s.host().CreateSession(ctx, req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kernelcallback: create session: %v", err)
	}
	// Auto-attach the calling frontend.
	release := s.scopes.Grant(sessionscope.KeyFor(s.producer), info.GetSessionId())
	s.trackAttach(info.GetSessionId(), release)
	return &kernelv1.CreateSessionResult{Info: info}, nil
}

// AttachSession grants the calling producer a scope for session_id when the
// session is live, and returns its SessionInfo.
func (s *Server) AttachSession(ctx context.Context, req *kernelv1.AttachSessionRequest) (*kernelv1.AttachSessionResult, error) {
	sessionID := req.GetSessionId()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "kernelcallback: attach session: session_id is required")
	}
	live, ok := s.sessions.Get(sessionID)
	if !ok {
		return nil, status.Error(codes.NotFound, "kernelcallback: attach session: session not found")
	}
	release := s.scopes.Grant(sessionscope.KeyFor(s.producer), sessionID)
	s.trackAttach(sessionID, release)

	meta, err := live.Meta(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kernelcallback: attach session: %v", err)
	}
	info := &sessionv1.SessionInfo{
		SessionId: meta.SessionID,
		Profile:   meta.Profile,
		Status:    meta.Status,
		Depth:     int32(meta.Depth), // #nosec G115 -- session-tree depth is a tiny bounded count
		StartedAt: timestamppb.New(meta.StartedAt),
	}
	if meta.ParentSessionID != "" {
		info.ParentSessionId = &meta.ParentSessionID
	}
	return &kernelv1.AttachSessionResult{Info: info}, nil
}

// ResumeSession currently behaves like AttachSession; re-open semantics
// for terminal sessions land with the session runner.
func (s *Server) ResumeSession(ctx context.Context, req *kernelv1.ResumeSessionRequest) (*kernelv1.ResumeSessionResult, error) {
	res, err := s.AttachSession(ctx, &kernelv1.AttachSessionRequest{SessionId: req.GetSessionId()})
	if err != nil {
		return nil, err
	}
	return &kernelv1.ResumeSessionResult{Info: res.GetInfo()}, nil
}

// DetachSession drops one outstanding grant for session_id that this
// Server's AttachSession previously took.
func (s *Server) DetachSession(_ context.Context, req *kernelv1.DetachSessionRequest) (*kernelv1.DetachSessionResult, error) {
	sessionID := req.GetSessionId()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "kernelcallback: detach session: session_id is required")
	}
	s.releaseAttach(sessionID)
	return &kernelv1.DetachSessionResult{}, nil
}

// ListSessions returns a filtered session summary list.
func (s *Server) ListSessions(ctx context.Context, req *kernelv1.ListSessionsRequest) (*kernelv1.ListSessionsResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: ListSessions not implemented")
	}
	sessions, err := s.host().ListSessions(ctx, req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kernelcallback: list sessions: %v", err)
	}
	return &kernelv1.ListSessionsResult{Sessions: sessions}, nil
}

// InvokeSlashCommand dispatches a slash command.
func (s *Server) InvokeSlashCommand(ctx context.Context, req *kernelv1.InvokeSlashCommandRequest) (*kernelv1.InvokeSlashCommandResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: InvokeSlashCommand not implemented")
	}
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if err := s.host().InvokeSlashCommand(ctx, req); err != nil {
		return nil, mapHostErr(err)
	}
	return &kernelv1.InvokeSlashCommandResult{}, nil
}

// TriggerAction dispatches an ActionNode activation.
func (s *Server) TriggerAction(ctx context.Context, req *kernelv1.TriggerActionRequest) (*kernelv1.TriggerActionResult, error) {
	if s.host() == nil {
		return nil, status.Error(codes.Unimplemented, "kernelcallback: TriggerAction not implemented")
	}
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	if err := s.host().TriggerAction(ctx, req); err != nil {
		return nil, mapHostErr(err)
	}
	return &kernelv1.TriggerActionResult{}, nil
}

func mapHostErr(err error) error {
	if err == nil {
		return nil
	}
	// Preserve FailedPrecondition / NotFound style when the host surfaces them.
	if st, ok := status.FromError(err); ok {
		return st.Err()
	}
	return status.Errorf(codes.FailedPrecondition, "kernelcallback: %v", err)
}

// StreamDeltas is the live-only token fast path. When no DeltaHub is
// configured the stream stays open until ctx is canceled (no deltas).
func (s *Server) StreamDeltas(req *kernelv1.StreamDeltasRequest, stream kernelv1.KernelCallbackService_StreamDeltasServer) error {
	ctx := stream.Context()
	if _, err := s.authorizedSession(ctx, req.GetSessionId()); err != nil {
		return err
	}
	if s.deltas == nil {
		<-ctx.Done()
		return nil
	}
	return s.deltas.Serve(ctx, req.GetSessionId(), stream)
}

// trackAttach records the release func AttachSession obtained so
// DetachSession can drop exactly one grant.
func (s *Server) trackAttach(sessionID string, release func()) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.attachReleases[sessionID] = append(s.attachReleases[sessionID], release)
}

// releaseAttach pops and runs one tracked release for sessionID, if any.
func (s *Server) releaseAttach(sessionID string) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	list := s.attachReleases[sessionID]
	if len(list) == 0 {
		return
	}
	release := list[len(list)-1]
	s.attachReleases[sessionID] = list[:len(list)-1]
	if len(s.attachReleases[sessionID]) == 0 {
		delete(s.attachReleases, sessionID)
	}
	release()
}
