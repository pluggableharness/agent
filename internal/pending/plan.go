package pending

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pluggableharness/agent/internal/plandecision"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

// ErrAlreadyResolved reports a second Answer for an id that was already
// completed, or a send that lost a race.
var ErrAlreadyResolved = errors.New("pending: already resolved")

// ErrNoWaiter reports Answer for an id with no outstanding Resolve.
var ErrNoWaiter = errors.New("pending: no pending waiter")

// PlanBridge implements plandecision.Resolver by parking on Answer.
type PlanBridge struct {
	mu      sync.Mutex
	waiters map[string]chan planResult // key: sessionID + "\x00" + planItemID
}

type planResult struct {
	dec plandecision.Decision
	err error
}

// NewPlanBridge returns an empty bridge.
func NewPlanBridge() *PlanBridge {
	return &PlanBridge{waiters: make(map[string]chan planResult)}
}

func planKey(sessionID, planItemID string) string {
	return sessionID + "\x00" + planItemID
}

// Resolve implements plandecision.Resolver: blocks until Answer or ctx cancel.
func (b *PlanBridge) Resolve(ctx context.Context, req plandecision.Request) (plandecision.Decision, error) {
	if err := req.Validate(); err != nil {
		return plandecision.Decision{}, err
	}
	key := planKey(req.SessionID, req.Item.GetId())
	ch := make(chan planResult, 1)

	b.mu.Lock()
	if _, exists := b.waiters[key]; exists {
		b.mu.Unlock()
		return plandecision.Decision{}, fmt.Errorf("pending: plan: duplicate waiter for %q", req.Item.GetId())
	}
	b.waiters[key] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiters, key)
		b.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return plandecision.Decision{}, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return plandecision.Decision{}, res.err
		}
		if err := plandecision.ValidateDecision(req, res.dec); err != nil {
			return plandecision.Decision{}, err
		}
		return res.dec, nil
	}
}

// Answer completes one outstanding Resolve. First call wins.
func (b *PlanBridge) Answer(sessionID, planItemID string, dec plandecision.Decision) error {
	key := planKey(sessionID, planItemID)
	b.mu.Lock()
	ch, ok := b.waiters[key]
	if !ok {
		b.mu.Unlock()
		return ErrNoWaiter
	}
	delete(b.waiters, key)
	b.mu.Unlock()

	select {
	case ch <- planResult{dec: dec}:
		return nil
	default:
		return ErrAlreadyResolved
	}
}

// ClientDecisionToTerminal maps an operator allow/deny to a plan decision.
func ClientDecisionToTerminal(d planv1.ClientDecision) (planv1.PlanDecision, error) {
	switch d {
	case planv1.ClientDecision_CLIENT_DECISION_ALLOW:
		return planv1.PlanDecision_PLAN_DECISION_ALLOW, nil
	case planv1.ClientDecision_CLIENT_DECISION_DENY:
		return planv1.PlanDecision_PLAN_DECISION_DENY, nil
	default:
		return planv1.PlanDecision_PLAN_DECISION_UNSPECIFIED, fmt.Errorf("pending: invalid client decision %v", d)
	}
}

var _ plandecision.Resolver = (*PlanBridge)(nil)
