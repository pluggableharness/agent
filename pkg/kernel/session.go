package kernel

import (
	"context"
	"fmt"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// RunSession dispatches a nested sub-agent session under a named agent.hcl
// profile, blocking until the child session reaches a terminal status. Full
// turn-by-turn semantics — profile resolution, budget inheritance,
// visibility of intermediate turns to the parent — live in
// agent-loop/subagents.md; kernel-callbacks.md#the-callback-channel gives
// only this RPC's wire-level calling contract.
//
// RunSession is session-scoped via req.ParentSessionId rather than a field
// literally named session_id: it both identifies the calling (parent)
// session and creates a new child, so kernel-callbacks.md names the field
// for the relationship it establishes. req.RemainingDepth and
// req.RemainingCostBudgetUsd MUST be set to the caller's own inherited,
// only-shrinking budgets — the child is never able to widen either, only
// spend down what it was given. req is passed through directly: between
// Profile, Prompt, ParentSessionId, RemainingDepth, and
// RemainingCostBudgetUsd, this request has more required fields than an
// options-struct-of-parameters could hold without becoming its own
// re-implementation of the generated type.
//
// The result's TotalCostUsd/TotalInputTokens/TotalOutputTokens are a
// whole-session rollup summed across every turn the child ran (including
// its own descendant sub-agent sessions) — a different shape from one
// completion call's per-call Usage — letting a caller do budget-aware
// fan-out without separately re-summing the child's event history itself.
func (c *Client) RunSession(ctx context.Context, req *kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error) {
	result, err := c.raw.RunSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("kernel: run session: %w", err)
	}
	return result, nil
}

// GetSession returns the calling plugin's own session's metadata plus its
// live, in-memory budget rollups — the same state-backend.md-backed
// SessionInfo the frontend protocol already uses, extended with two fields
// state backend deliberately never persists (kernel-callbacks.md#getsession).
//
// sessionID is mandatory and MUST name the session this plugin was
// actually invoked for, the same one-session-only rule Emit documents.
// The result's Info.CostUsd is the persisted cost_ledger SUM — read back,
// never re-walked and re-summed here — while RemainingDepth and
// RemainingCostBudgetUsd are live, in-memory figures recomputed at spawn
// time and spent down at each RunSession hop; a caller cannot derive
// either of those two from Info alone.
func (c *Client) GetSession(ctx context.Context, sessionID string) (*kernelv1.GetSessionResult, error) {
	result, err := c.raw.GetSession(ctx, &kernelv1.GetSessionRequest{SessionId: sessionID})
	if err != nil {
		return nil, fmt.Errorf("kernel: get session: %w", err)
	}
	return result, nil
}
