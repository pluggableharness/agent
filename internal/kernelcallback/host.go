package kernelcallback

import (
	"context"
	"sync/atomic"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// FrontendHost is the agent-loop surface KernelCallbackService frontend
// RPCs need. Implemented by internal/kernel's frontendHost.
type FrontendHost interface {
	CreateSession(ctx context.Context, req *kernelv1.CreateSessionRequest) (*sessionv1.SessionInfo, error)
	SubmitInput(ctx context.Context, sessionID string, content []*contentv1.ContentBlock) (turnID string, err error)
	Interrupt(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, req *kernelv1.ListSessionsRequest) ([]*sessionv1.SessionInfo, error)
	GetSessionState(ctx context.Context, sessionID string) (*sessionv1.SessionState, error)
	ResolvePlanDecision(ctx context.Context, req *kernelv1.ResolvePlanDecisionRequest) error
	ResolveInteractive(ctx context.Context, req *kernelv1.ResolveInteractiveRequest) error
	InvokeSlashCommand(ctx context.Context, req *kernelv1.InvokeSlashCommandRequest) error
	TriggerAction(ctx context.Context, req *kernelv1.TriggerActionRequest) error
}

// HostSlot is a late-bound FrontendHost shared by every per-plugin
// kernelcallback.Server. The agent-loop host is installed after plugins
// start (it needs the catalog and turn stack), so callback servers hold a
// slot rather than a concrete host at construction.
type HostSlot struct {
	v atomic.Value // stores FrontendHost
}

// Set installs host for every Server sharing this slot.
func (s *HostSlot) Set(host FrontendHost) {
	if s == nil {
		return
	}
	s.v.Store(host)
}

// Get returns the installed host, or nil.
func (s *HostSlot) Get() FrontendHost {
	if s == nil {
		return nil
	}
	v := s.v.Load()
	if v == nil {
		return nil
	}
	h, _ := v.(FrontendHost)
	return h
}
