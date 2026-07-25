package session

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/turn"
)

// storedStatus reads a finished session's persisted session_meta status
// back off disk, so a test asserts what a later reader actually sees
// rather than only what Run returned.
func storedStatus(t *testing.T, h *harness, sessionID string) sessionv1.SessionStatus {
	t.Helper()
	metas, err := h.store.List(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, meta := range metas {
		if meta.SessionID == sessionID {
			if meta.EndedAt == nil {
				t.Fatal("session_meta.ended_at must be set once a session ends")
			}
			return meta.Status
		}
	}
	t.Fatalf("session %s not found on disk", sessionID)
	return sessionv1.SessionStatus_SESSION_STATUS_UNSPECIFIED
}

func TestRunNormalSession(t *testing.T) {
	t.Parallel()

	h := newHarness(t, profileWith(func(p *agentprofile.AgentProfile) {
		p.Tools = []string{"filesystem.*"}
	}), []step{running(0.10, "a"), running(0.20, "b"), done(0.30)}, true)

	// Grants are released before Run returns, so liveness is asserted from
	// inside the loop, on the first turn.
	var grantedAtFirstTurn []bool
	h.turns.onCall = func(n int, req turn.Request) {
		if n != 0 {
			return
		}
		for _, key := range []sessionscope.Key{
			sessionscope.KeyFor(modelProducer()),
			sessionscope.KeyFor(toolProducer()),
			sessionscope.KeyFor(contextProducer()),
		} {
			grantedAtFirstTurn = append(grantedAtFirstTurn, h.scopes.Authorized(key, req.SessionID))
		}
	}

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "hello", WorkingDirectory: "/w"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("status: got %v, want COMPLETED", result.Status)
	}
	if result.FinalAnswerReason != "" {
		t.Fatalf("final answer reason: got %q, want empty", result.FinalAnswerReason)
	}
	if got, want := result.TotalCostUSD, 0.60; got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("total cost: got %v, want %v", got, want)
	}
	if result.TotalInputTokens != 30 || result.TotalOutputTokens != 15 {
		t.Fatalf("tokens: got %d/%d, want 30/15", result.TotalInputTokens, result.TotalOutputTokens)
	}
	if result.FinalMessage == nil {
		t.Fatal("final message must be set")
	}

	requests := h.turns.requests()
	if len(requests) != 3 {
		t.Fatalf("turn count: got %d, want 3", len(requests))
	}
	for i, req := range requests {
		if req.FinalAnswer {
			t.Fatalf("turn %d: FinalAnswer must be false in a normal session", i)
		}
		if req.TurnIndex != i {
			t.Fatalf("turn %d: TurnIndex is %d", i, req.TurnIndex)
		}
		if req.SessionID != result.SessionID || req.TurnID == "" {
			t.Fatalf("turn %d: ids are %q/%q", i, req.SessionID, req.TurnID)
		}
		if req.WorkingDirectory != "/w" {
			t.Fatalf("turn %d: working directory is %q", i, req.WorkingDirectory)
		}
		if len(req.ScopedTools) != 1 {
			t.Fatalf("turn %d: scoped tools are %v", i, req.ScopedTools)
		}
	}
	if got := requests[0].History; len(got) != 1 || got[0].GetContent()[0].GetText().GetText() != "hello" {
		t.Fatalf("first turn history: got %v, want the prompt alone", got)
	}
	if len(requests[2].History) != 3 {
		t.Fatalf("history carry-forward: turn 2 saw %d messages, want 3", len(requests[2].History))
	}

	for i, granted := range grantedAtFirstTurn {
		if !granted {
			t.Fatalf("provider %d held no grant during the session", i)
		}
	}
	if len(grantedAtFirstTurn) != 3 {
		t.Fatalf("checked %d grants, want 3", len(grantedAtFirstTurn))
	}
	assertNoOutstandingGrants(t, h.scopes, result.SessionID)

	if _, ok := h.table.Get(result.SessionID); ok {
		t.Fatal("live session must be unregistered after Run")
	}
	if got := storedStatus(t, h, result.SessionID); got != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("persisted status: got %v, want COMPLETED", got)
	}

	wantPoints := []commonv1.HookPoint{
		commonv1.HookPoint_HOOK_POINT_SESSION_START,
		commonv1.HookPoint_HOOK_POINT_SESSION_END,
	}
	got := h.hooks.dispatched()
	if len(got) != len(wantPoints) || got[0] != wantPoints[0] || got[1] != wantPoints[1] {
		t.Fatalf("hook points: got %v, want %v", got, wantPoints)
	}
	if h.hooks.starts[0].profile != DefaultProfileName || h.hooks.starts[0].workingDirectory != "/w" {
		t.Fatalf("session-start payload: %+v", h.hooks.starts[0])
	}
	if h.hooks.starts[0].hasParent {
		t.Fatal("session-start payload must leave parent_session_id unset for a root session")
	}
	if h.hooks.ends[0].status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("session-end payload: %+v", h.hooks.ends[0])
	}
}

func TestRunBoundsFireIndependently(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*agentprofile.AgentProfile)
		steps      []step
		advance    time.Duration
		wantStatus sessionv1.SessionStatus
		wantReason string
		wantTurns  int
	}{
		{
			name:       "max turns",
			mutate:     func(p *agentprofile.AgentProfile) { p.MaxTurns = 2 },
			steps:      []step{running(0, "a"), running(0, "b")},
			wantStatus: sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_TURNS,
			wantReason: ReasonMaxTurns,
			wantTurns:  3,
		},
		{
			name:       "max cost usd",
			mutate:     func(p *agentprofile.AgentProfile) { p.MaxCostUSD = 0.15 },
			steps:      []step{running(0.10, "a"), running(0.10, "b")},
			wantStatus: sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_BUDGET_USD,
			wantReason: ReasonMaxCostUSD,
			wantTurns:  3,
		},
		{
			name:       "max wall clock",
			mutate:     func(p *agentprofile.AgentProfile) { p.MaxWallClockS = 60 },
			steps:      []step{running(0, "a")},
			advance:    90 * time.Second,
			wantStatus: sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_WALL_CLOCK,
			wantReason: ReasonMaxWallClock,
			wantTurns:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, profileWith(tt.mutate), tt.steps, false)
			if tt.advance > 0 {
				h.turns.onCall = func(n int, _ turn.Request) {
					if n == 0 {
						h.clock.advance(tt.advance)
					}
				}
			}

			result, err := h.runner.Run(context.Background(), Spec{Prompt: "go"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Fatalf("status: got %v, want %v", result.Status, tt.wantStatus)
			}
			if result.FinalAnswerReason != tt.wantReason {
				t.Fatalf("reason: got %q, want %q", result.FinalAnswerReason, tt.wantReason)
			}

			requests := h.turns.requests()
			if len(requests) != tt.wantTurns {
				t.Fatalf("turn count: got %d, want %d", len(requests), tt.wantTurns)
			}
			final := requests[len(requests)-1]
			if !final.FinalAnswer {
				t.Fatal("the last turn must be the limit-reached final-answer turn")
			}
			if final.FinalAnswerReason != tt.wantReason {
				t.Fatalf("final answer reason: got %q, want %q", final.FinalAnswerReason, tt.wantReason)
			}
			for i, req := range requests[:len(requests)-1] {
				if req.FinalAnswer {
					t.Fatalf("turn %d must not be a final-answer turn", i)
				}
			}
			if got := storedStatus(t, h, result.SessionID); got != tt.wantStatus {
				t.Fatalf("persisted status: got %v, want %v", got, tt.wantStatus)
			}
			assertNoOutstandingGrants(t, h.scopes, result.SessionID)
		})
	}
}

func TestRunDoomLoopRoutesThroughFinalAnswer(t *testing.T) {
	t.Parallel()

	// Three turns' worth of the identical call hash is exactly
	// doomloop.DefaultConfig's threshold.
	h := newHarness(t, profileWith(func(*agentprofile.AgentProfile) {}),
		[]step{running(0, "same")}, false)

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "loop"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("status: got %v, want COMPLETED", result.Status)
	}
	if result.FinalAnswerReason != ReasonDoomLoop {
		t.Fatalf("reason: got %q, want %q", result.FinalAnswerReason, ReasonDoomLoop)
	}

	requests := h.turns.requests()
	if len(requests) != 4 {
		t.Fatalf("turn count: got %d, want 4 (3 identical + 1 final answer)", len(requests))
	}
	final := requests[3]
	if !final.FinalAnswer || final.FinalAnswerReason != ReasonDoomLoop {
		t.Fatalf("final turn: %+v", final)
	}
	if got := storedStatus(t, h, result.SessionID); got != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("persisted status: got %v, want COMPLETED", got)
	}
}

func TestRunCircuitBreakerTripRoutesThroughFinalAnswer(t *testing.T) {
	t.Parallel()

	tripped := step{result: turn.Result{TrippedProviders: []string{"filesystem"}}}
	h := newHarness(t, profileWith(func(*agentprofile.AgentProfile) {}), []step{tripped}, false)

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "deny me"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("status: got %v, want COMPLETED", result.Status)
	}
	if result.FinalAnswerReason != ReasonCircuitBreaker {
		t.Fatalf("reason: got %q, want %q", result.FinalAnswerReason, ReasonCircuitBreaker)
	}

	requests := h.turns.requests()
	if len(requests) != 2 {
		t.Fatalf("turn count: got %d, want 2", len(requests))
	}
	if !requests[1].FinalAnswer || requests[1].FinalAnswerReason != ReasonCircuitBreaker {
		t.Fatalf("final turn: %+v", requests[1])
	}
}

func TestRunCancellationMidLoop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := newHarness(t, profileWith(func(*agentprofile.AgentProfile) {}),
		[]step{running(0.05, "a"), {err: context.Canceled}}, false)
	h.turns.onCall = func(n int, _ turn.Request) {
		if n == 0 {
			cancel()
		}
	}

	result, err := h.runner.Run(ctx, Spec{Prompt: "interrupt me"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: got %v, want context.Canceled", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_CANCELLED {
		t.Fatalf("status: got %v, want CANCELLED", result.Status)
	}
	if got := storedStatus(t, h, result.SessionID); got != sessionv1.SessionStatus_SESSION_STATUS_CANCELLED {
		t.Fatalf("persisted status: got %v, want CANCELLED (finalize must outlive cancellation)", got)
	}
	if h.hooks.ends[0].status != sessionv1.SessionStatus_SESSION_STATUS_CANCELLED {
		t.Fatalf("session-end payload: %+v", h.hooks.ends[0])
	}
	assertNoOutstandingGrants(t, h.scopes, result.SessionID)
}

func TestRunTurnFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("model provider exploded")
	h := newHarness(t, profileWith(func(*agentprofile.AgentProfile) {}), []step{{err: boom}}, false)

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "break"})
	if !errors.Is(err, boom) {
		t.Fatalf("Run: got %v, want the turn driver's error", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_FAILED {
		t.Fatalf("status: got %v, want FAILED", result.Status)
	}
	if got := storedStatus(t, h, result.SessionID); got != sessionv1.SessionStatus_SESSION_STATUS_FAILED {
		t.Fatalf("persisted status: got %v, want FAILED", got)
	}
	assertNoOutstandingGrants(t, h.scopes, result.SessionID)
}

func TestRunFinalAnswerTurnFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("final answer turn exploded")
	h := newHarness(t, profileWith(func(p *agentprofile.AgentProfile) { p.MaxTurns = 1 }),
		[]step{running(0, "a"), {err: boom}}, false)

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "break late"})
	if !errors.Is(err, boom) {
		t.Fatalf("Run: got %v, want the turn driver's error", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_FAILED {
		t.Fatalf("status: got %v, want FAILED", result.Status)
	}
	if result.FinalAnswerReason != ReasonMaxTurns {
		t.Fatalf("reason: got %q, want %q", result.FinalAnswerReason, ReasonMaxTurns)
	}
}

func TestRunDoneWinsOverAFiredBound(t *testing.T) {
	t.Parallel()

	// The model finishes on exactly the last permitted turn: no wasted
	// final-answer turn, and the session completes rather than reporting
	// error_max_turns.
	h := newHarness(t, profileWith(func(p *agentprofile.AgentProfile) { p.MaxTurns = 2 }),
		[]step{running(0, "a"), done(0)}, false)

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "just in time"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("status: got %v, want COMPLETED", result.Status)
	}
	if len(h.turns.requests()) != 2 {
		t.Fatalf("turn count: got %d, want 2", len(h.turns.requests()))
	}
}

func TestRunImplicitDefaultProfile(t *testing.T) {
	t.Parallel()

	// No agent_profile "default" block at all: the builtin defaults apply,
	// the sole loaded model is routed to, and no tools are in scope.
	h := newHarness(t, nil, []step{done(0)}, true)

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "bare config"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("status: got %v, want COMPLETED", result.Status)
	}

	req := h.turns.requests()[0]
	if len(req.ScopedTools) != 0 {
		t.Fatalf("scoped tools: got %v, want none (strict default)", req.ScopedTools)
	}
	if req.Model.Ref != testModelRef {
		t.Fatalf("model: got %+v, want %+v", req.Model.Ref, testModelRef)
	}
	if h.hooks.starts[0].profile != DefaultProfileName {
		t.Fatalf("session-start profile: got %q", h.hooks.starts[0].profile)
	}
}

func TestRunPlanModeThreadsThrough(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, []step{done(0)}, false)
	if _, err := h.runner.Run(context.Background(), Spec{Prompt: "plan", PlanMode: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !h.turns.requests()[0].PlanMode {
		t.Fatal("PlanMode must reach the turn request")
	}
}

func TestRunResolutionFailureLeavesNoGrantsAndNoSession(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, nil, false)

	result, err := h.runner.Run(context.Background(), Spec{Profile: "nope", Prompt: "x"})
	if !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("Run: got %v, want ErrUnknownProfile", err)
	}
	if result.SessionID != "" {
		t.Fatalf("session id: got %q, want empty on a pre-creation failure", result.SessionID)
	}
	if len(h.turns.requests()) != 0 {
		t.Fatal("no turn may run when resolution fails")
	}
	if len(h.hooks.dispatched()) != 0 {
		t.Fatal("no hook may fire when resolution fails")
	}
	assertNoOutstandingGrants(t, h.scopes, "")

	metas, err := h.store.List(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("session files: got %d, want 0", len(metas))
	}
}

func TestRunSurvivesHookDispatchFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, []step{done(0)}, false)
	h.hooks.err = errors.New("subscriber chain broke")

	result, err := h.runner.Run(context.Background(), Spec{Prompt: "hooks are broken"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != sessionv1.SessionStatus_SESSION_STATUS_COMPLETED {
		t.Fatalf("status: got %v, want COMPLETED", result.Status)
	}
	if len(h.hooks.dispatched()) != 2 {
		t.Fatalf("hook points: got %v, want both dispatched despite the error", h.hooks.dispatched())
	}
}

func TestBoundReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   bounds.Fired
		want string
	}{
		{"none", bounds.FiredNone, ""},
		{"max turns", bounds.FiredMaxTurns, ReasonMaxTurns},
		{"max cost", bounds.FiredMaxCostUSD, ReasonMaxCostUSD},
		{"max wall clock", bounds.FiredMaxWallClock, ReasonMaxWallClock},
		{"unknown", bounds.Fired(99), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := boundReason(tt.in); got != tt.want {
				t.Fatalf("boundReason: got %q, want %q", got, tt.want)
			}
		})
	}
}
