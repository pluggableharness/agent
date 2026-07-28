package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/kernelcallback"
	"github.com/pluggableharness/agent/internal/pending"
	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/session"
)

// frontendHost implements kernelcallback.FrontendHost over a session.Runner
// and the pending bridges that complete plan/interactive resolutions.
type frontendHost struct {
	k *kernel

	mu      sync.Mutex
	runner  *session.Runner
	handles map[string]*session.Handle // sessionID -> handle
	plans   *pending.PlanBridge
	inter   *pending.InteractiveBridge
}

func newFrontendHost(k *kernel, runner *session.Runner, plans *pending.PlanBridge, inter *pending.InteractiveBridge) *frontendHost {
	return &frontendHost{
		k:       k,
		runner:  runner,
		handles: make(map[string]*session.Handle),
		plans:   plans,
		inter:   inter,
	}
}

var _ kernelcallback.FrontendHost = (*frontendHost)(nil)

func (h *frontendHost) CreateSession(ctx context.Context, req *kernelv1.CreateSessionRequest) (*sessionv1.SessionInfo, error) {
	profile := ""
	if req.Profile != nil {
		profile = *req.Profile
	}
	wd := h.k.opts.WorkingDirectory
	if req.WorkingDirectory != nil && *req.WorkingDirectory != "" {
		wd = *req.WorkingDirectory
	}
	initial := ""
	if req.InitialPrompt != nil {
		initial = *req.InitialPrompt
	}

	// Open with empty prompt so history is empty; Submit the initial
	// prompt below when present (avoids double-seeding).
	handle, err := h.runner.Open(ctx, session.Spec{
		Profile:          profile,
		WorkingDirectory: wd,
	})
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.handles[handle.SessionID()] = handle
	h.mu.Unlock()

	if initial != "" {
		// The first turn outlives this RPC, so it detaches from ctx's
		// cancellation — but WithoutCancel, never Background: a fresh root
		// context severs trace parentage silently, so every session opened
		// with a prompt would lose its first turn from the trace
		// (logging-telemetry.md, go-architecture.md's WithoutCancel rule).
		submitCtx := context.WithoutCancel(ctx)
		go func() {
			if _, err := handle.Submit(submitCtx, []*contentv1.ContentBlock{
				{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: initial}}},
			}); err != nil {
				h.k.logger.ErrorContext(submitCtx, "kernel: initial prompt submit failed",
					"session_id", handle.SessionID(), "err", err)
			}
		}()
	}

	return handle.Info(ctx)
}

func (h *frontendHost) SubmitInput(ctx context.Context, sessionID string, content []*contentv1.ContentBlock) (string, error) {
	handle, err := h.handle(sessionID)
	if err != nil {
		return "", err
	}
	return handle.Submit(ctx, content)
}

func (h *frontendHost) Interrupt(_ context.Context, sessionID string) error {
	handle, err := h.handle(sessionID)
	if err != nil {
		return err
	}
	handle.Interrupt()
	return nil
}

func (h *frontendHost) ListSessions(ctx context.Context, req *kernelv1.ListSessionsRequest) ([]*sessionv1.SessionInfo, error) {
	metas, err := h.k.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*sessionv1.SessionInfo, 0, len(metas))
	for _, m := range metas {
		if req.GetRootsOnly() && m.ParentSessionID != "" {
			continue
		}
		if req.ParentSessionId != nil && m.ParentSessionID != *req.ParentSessionId {
			continue
		}
		if req.Status != nil && m.Status != *req.Status {
			continue
		}
		info := &sessionv1.SessionInfo{
			SessionId: m.SessionID,
			Profile:   m.Profile,
			Status:    m.Status,
			Depth:     int32(m.Depth), // #nosec G115
			StartedAt: timestamppb.New(m.StartedAt),
		}
		if m.ParentSessionID != "" {
			info.ParentSessionId = &m.ParentSessionID
		}
		if m.EndedAt != nil {
			info.EndedAt = timestamppb.New(*m.EndedAt)
		}
		out = append(out, info)
	}
	return out, nil
}

func (h *frontendHost) GetSessionState(ctx context.Context, sessionID string) (*sessionv1.SessionState, error) {
	handle, err := h.handle(sessionID)
	if err != nil {
		return nil, err
	}
	return handle.State(ctx)
}

func (h *frontendHost) ResolvePlanDecision(_ context.Context, req *kernelv1.ResolvePlanDecisionRequest) error {
	terminal, err := pending.ClientDecisionToTerminal(req.GetDecision())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	scope := req.GetScope()
	if scope == planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_UNSPECIFIED {
		scope = planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE
	}
	dec := plandecision.Decision{
		Decision:       terminal,
		Scope:          scope,
		CorrectedInput: req.CorrectedInput,
		DecidedBy:      "operator",
	}
	if err := h.plans.Answer(req.GetSessionId(), req.GetPlanItemId(), dec); err != nil {
		if errors.Is(err, pending.ErrNoWaiter) || errors.Is(err, pending.ErrAlreadyResolved) {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		return err
	}
	return nil
}

func (h *frontendHost) ResolveInteractive(_ context.Context, req *kernelv1.ResolveInteractiveRequest) error {
	if err := h.inter.Complete(req.GetCallId(), req.GetResponse()); err != nil {
		if errors.Is(err, pending.ErrNoWaiter) || errors.Is(err, pending.ErrAlreadyResolved) {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		return err
	}
	return nil
}

func (h *frontendHost) InvokeSlashCommand(ctx context.Context, req *kernelv1.InvokeSlashCommandRequest) error {
	// Expand to a user message so the model (or a future dedicated slash
	// path) can act. Dedicated slashcommand.Invoke is a later refinement.
	text := "/" + req.GetName()
	if args := req.GetArgs(); args != "" {
		text += " " + args
	}
	_, err := h.SubmitInput(ctx, req.GetSessionId(), []*contentv1.ContentBlock{
		{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}}},
	})
	return err
}

func (h *frontendHost) TriggerAction(ctx context.Context, req *kernelv1.TriggerActionRequest) error {
	if h.k.catalog == nil {
		return status.Error(codes.FailedPrecondition, "kernel: catalog not ready")
	}
	th, err := h.k.catalog.Tool(req.GetProvider(), req.GetToolName())
	if err != nil {
		return status.Errorf(codes.NotFound, "kernel: tool %s.%s: %v", req.GetProvider(), req.GetToolName(), err)
	}
	input := req.GetArgs()
	if input == nil {
		input, _ = structpb.NewStruct(nil)
	}
	stream, err := th.Client.Invoke(ctx, &toolv1.InvokeRequest{
		Call: &toolv1.ToolCall{
			Id:        req.GetNodeId(),
			ToolName:  req.GetToolName(),
			Arguments: input,
			CallContext: &commonv1.CallContext{
				SessionId: req.GetSessionId(),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("kernel: trigger action invoke: %w", err)
	}
	for {
		_, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("kernel: trigger action stream: %w", recvErr)
		}
	}
}

func (h *frontendHost) handle(sessionID string) (*session.Handle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	handle, ok := h.handles[sessionID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "kernel: no interactive handle for session %s", sessionID)
	}
	return handle, nil
}
