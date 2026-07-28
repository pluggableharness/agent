package kernel

import (
	"context"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/interactive/drivers/unattended"
	"github.com/pluggableharness/agent/internal/pending"
	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/autoallow"
)

// planResolver routes ASK decisions to the pending PlanBridge when the
// session was opened interactively (frontend host has a handle), and to
// autoallow for CLI-driven Runner.Run sessions.
type planResolver struct {
	k        *kernel
	bridge   *pending.PlanBridge
	fallback plandecision.Resolver
}

func newPlanResolver(k *kernel) (plandecision.Resolver, error) {
	fb, err := autoallow.New(autoallow.Config{
		AcknowledgeUnsafeAutoAllow: true,
		Logger:                     k.logger,
		Telemetry:                  k.telem,
	})
	if err != nil {
		return nil, err
	}
	return &planResolver{k: k, bridge: k.plans, fallback: fb}, nil
}

func (p *planResolver) Resolve(ctx context.Context, req plandecision.Request) (plandecision.Decision, error) {
	if p.interactive(req.SessionID) {
		return p.bridge.Resolve(ctx, req)
	}
	return p.fallback.Resolve(ctx, req)
}

func (p *planResolver) interactive(sessionID string) bool {
	host, ok := p.k.hostSlot.Get().(*frontendHost)
	if !ok || host == nil {
		return false
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	_, ok = host.handles[sessionID]
	return ok
}

// interactiveResolver routes interactive tool calls similarly.
type interactiveResolver struct {
	k        *kernel
	bridge   *pending.InteractiveBridge
	fallback interactive.Resolver
}

func newInteractiveResolver(k *kernel) interactive.Resolver {
	return &interactiveResolver{
		k:        k,
		bridge:   k.inter,
		fallback: unattended.New(k.logger, k.telem),
	}
}

func (r *interactiveResolver) Resolve(ctx context.Context, req interactive.Request) (interactive.Response, error) {
	// Interactive requests lack session id on the Request type; prefer the
	// bridge whenever any interactive handle exists (frontend attached).
	if r.anyInteractive() {
		return r.bridge.Resolve(ctx, req)
	}
	return r.fallback.Resolve(ctx, req)
}

func (r *interactiveResolver) anyInteractive() bool {
	host, ok := r.k.hostSlot.Get().(*frontendHost)
	if !ok || host == nil {
		return false
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	return len(host.handles) > 0
}
