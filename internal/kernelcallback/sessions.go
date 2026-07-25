package kernelcallback

import (
	"context"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// errNotAuthorized is the single error value every session-authorization
// failure returns, regardless of *why* it failed — see authorizedSession's
// doc comment for why the two failure modes it guards must never be
// distinguishable from one another at the gRPC-status level.
var errNotAuthorized = status.Error(codes.PermissionDenied, "kernelcallback: not authorized for this session")

// authorizedSession is the shared gate every session-scoped RPC (Emit,
// ReadEvents, GetSession) goes through before touching a session's data —
// kernel-callbacks.md's own MUST: "the kernel MUST reject a call naming
// any session other than the one the calling plugin was actually invoked
// for."
//
// It fails exactly the same way — codes.PermissionDenied, never
// codes.NotFound, via the shared errNotAuthorized value — whether:
//  1. s.scopes never granted this Server's producer a grant for
//     sessionID at all, or
//  2. it did, but the session is no longer live (s.sessions.Get misses):
//     the grant existed but the session already ended between the grant
//     and this call.
//
// This indistinguishability is deliberate, not an oversight to "fix" into
// two error codes later: a codes.NotFound (or any other code/message that
// let a caller tell the two apart) would let a caller probe for the
// existence of sessions it has no business knowing about, defeating the
// authorization check's own purpose. See CLAUDE.md.
func (s *Server) authorizedSession(_ context.Context, sessionID string) (*sessionstate.Live, error) {
	if sessionID == "" {
		return nil, errNotAuthorized
	}
	key := sessionscope.KeyFor(s.producer)
	if !s.scopes.Authorized(key, sessionID) {
		return nil, errNotAuthorized
	}
	live, ok := s.sessions.Get(sessionID)
	if !ok {
		return nil, errNotAuthorized
	}
	return live, nil
}

// rootSessionRemainingDepth is the fixed placeholder GetSession reports as
// RemainingDepth. This build is root-sessions-only — RunSession
// (server.go) is still deliberately codes.Unimplemented, so nothing
// anywhere in this codebase yet tracks a live, per-session depth budget
// the way bounds.Tracker already does for cost (internal/agentprofile's
// RootRemainingDepth/ChildRemainingDepth compute the *number* per
// configuration.md §8.4, but nothing wires a live tracker instance a
// GetSession call could read from yet). Reporting the honest
// "effectively unbounded" sentinel here is deliberately safer than
// fabricating a specific ceiling this build has no mechanism to enforce —
// a future phase that adds real depth-budget tracking replaces this
// constant with a live read from that tracker, at which point this
// comment (and CLAUDE.md's matching note) should go. See CLAUDE.md.
const rootSessionRemainingDepth = math.MaxInt32

// GetSession implements the GetSession RPC (kernel-callbacks.md's
// GetSession): the persisted half of the result (Info) comes from the
// authorized session's session_meta row and cost rollup, read via
// sessionstate.Live's thin pass-throughs to *statebackend.Session; the
// live half (RemainingCostBudgetUsd) comes from that session's in-memory
// *bounds.Tracker (state-backend.md's live-vs-post-hoc distinction).
// RemainingDepth is the rootSessionRemainingDepth placeholder documented
// above.
func (s *Server) GetSession(ctx context.Context, req *kernelv1.GetSessionRequest) (*kernelv1.GetSessionResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackGetSession(ctx, req.GetSessionId(), s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: get_session", "session_id", req.GetSessionId())

	live, err := s.authorizedSession(ctx, req.GetSessionId())
	if err != nil {
		s.logger.WarnContext(ctx, "kernelcallback: get_session: rejected", "err", err)
		return nil, err
	}

	meta, metaErr := live.Meta(ctx)
	if metaErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: get_session: %v", metaErr)
		s.logger.ErrorContext(ctx, "kernelcallback: get_session: meta query failed", "err", metaErr)
		return nil, err
	}
	totalCostUSD, costErr := live.TotalCostUSD(ctx)
	if costErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: get_session: %v", costErr)
		s.logger.ErrorContext(ctx, "kernelcallback: get_session: cost query failed", "err", costErr)
		return nil, err
	}

	info := &sessionv1.SessionInfo{
		SessionId: meta.SessionID,
		Profile:   meta.Profile,
		Status:    meta.Status,
		Depth:     int32(meta.Depth), // #nosec G115 -- meta.Depth is a session-tree nesting depth, always a tiny bounded count (configuration.md §8.4's max_depth), never attacker-controlled or anywhere near int32's range
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

	return &kernelv1.GetSessionResult{
		Info:                   info,
		RemainingDepth:         rootSessionRemainingDepth,
		RemainingCostBudgetUsd: live.Budget().RemainingCostUSD(),
	}, nil
}
