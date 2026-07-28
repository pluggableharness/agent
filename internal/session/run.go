package session

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/metric"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/doomloop"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/turn"
)

// run is one session's mutable state, held for the duration of one Run
// call and never on the Runner itself — what keeps concurrent Run calls
// safe.
type run struct {
	sessionID string
	spec      Spec
	res       resolution
	budget    *bounds.Tracker
	doom      *doomloop.Detector
	startedAt time.Time

	history   []*contentv1.Message
	turnIndex int
	assembled int64
	final     *contentv1.Message
	inTokens  int64
	outTokens int64

	// Vendor-reported state from the most recent completion, surfaced on
	// SessionState. Last-write-wins rather than accumulated: each is a
	// reading of "how is the vendor serving me right now", and an older
	// reading is not additive with a newer one the way token counts are.
	//
	// quotas is left untouched by a turn that reported none, so a
	// mid-session completion whose headers omitted budgets does not blank
	// a figure the operator was watching.
	quotas      []*modelv1.RateLimitSnapshot
	vendorCost  *modelv1.VendorCost
	actualModel string
}

// Run executes one whole session per turn-algorithm.md and returns its
// outcome.
//
// The returned error is non-nil only when the session could not be run at
// all (profile/model/tool resolution, or creating the session file), when
// a turn failed outright (Status is SESSION_STATUS_FAILED), or when the
// caller's context was canceled (Status is SESSION_STATUS_CANCELLED and
// the error is ctx.Err(), returned so an `err != nil` caller still notices
// — a canceled session is normal control flow per .claude/rules/grpc.md
// and is never logged at ERROR). A fired bound, a tripped doom loop, and a
// tripped circuit breaker are all ordinary outcomes reported on
// Result.Status with a nil error.
func (r *Runner) Run(ctx context.Context, spec Spec) (Result, error) {
	res, err := r.resolve(spec)
	if err != nil {
		return Result{}, err
	}

	startedAt := r.clock()
	sessionID := statebackend.NewSessionID(startedAt)

	ctx, span := r.telem.StartSession(ctx, telemetry.SessionSpan{
		SessionID:     sessionID,
		RootSessionID: sessionID,
		AgentProfile:  res.profileName,
	})
	var runErr error
	defer func() { telemetry.EndSpan(span, runErr) }()

	sess, err := r.store.Create(ctx, statebackend.SessionMeta{
		SessionID: sessionID,
		Profile:   res.profileName,
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: startedAt,
	})
	if err != nil {
		runErr = fmt.Errorf("session: create: %w", err)
		return Result{}, runErr
	}

	// NewLive takes no context by design (it wraps an already-open session
	// handle); its only Background use is the telemetry fallback for a nil
	// Provider, which r.telem never is.
	live := sessionstate.NewLive(sess, r.bus, res.limits, nil, r.clock, r.telem, r.logger) //nolint:contextcheck // see comment above
	r.sessions.Put(sessionID, live)

	// Every grant taken below is released by this defer on EVERY exit
	// path — normal completion, a fired bound, a turn failure,
	// cancellation, or a panic unwinding through Run.
	releases := make([]func(), 0, len(res.keys))
	for _, key := range res.keys {
		releases = append(releases, r.scopes.Grant(key, sessionID))
	}
	// teardown deliberately takes no context: statebackend.Session.Close
	// derives its own, precisely so a canceled caller context cannot
	// prevent a session file from being checkpointed and closed.
	defer r.teardown(sessionID, live, releases) //nolint:contextcheck // see comment above

	result, err := r.session(ctx, &run{
		sessionID: sessionID,
		spec:      spec,
		res:       res,
		budget:    live.Budget(),
		doom:      r.newDetector(),
		startedAt: startedAt,
		history:   []*contentv1.Message{userMessage(r.clock(), spec.Prompt)},
	}, sess)
	runErr = err
	return result, err
}

// newDetector builds this session's doom-loop detector. The config was
// already validated by New, so the error branch is unreachable — it is
// still handled rather than discarded, falling back to the canonical
// default rather than running with a nil detector.
func (r *Runner) newDetector() *doomloop.Detector {
	detector, err := doomloop.New(r.doomLoop)
	if err != nil {
		detector, _ = doomloop.New(doomloop.DefaultConfig)
	}
	return detector
}

// session runs the lifecycle between session creation and session
// teardown: session-start, the turn loop, then the terminal status and
// session-end.
func (r *Runner) session(ctx context.Context, st *run, sess *statebackend.Session) (Result, error) {
	r.telem.Instruments().SessionsStarted.Add(ctx, 1)
	r.telem.Instruments().ActiveSessions.Add(ctx, 1)
	defer r.telem.Instruments().ActiveSessions.Add(ctx, -1)

	r.logger.InfoContext(ctx, "session: starting",
		"session_id", st.sessionID,
		"profile", st.res.profileName,
		"model_id", st.res.model.Ref.ID,
		"tool_count", len(st.res.tools),
		"remaining_depth", st.res.remainingDepth)

	r.dispatchSessionStart(ctx, st)

	status, reason, loopErr := r.loop(ctx, st)

	// The finalize path deliberately runs under a context detached from
	// the caller's cancellation: a canceled session still MUST reach
	// session-end with status = cancelled, durably recorded to its own
	// session_meta row (subagents.md#cancellation-propagation), which is
	// impossible on a context that is already Done.
	finCtx := context.WithoutCancel(ctx)
	r.finalize(finCtx, st, sess, status, reason)

	return Result{
		SessionID:         st.sessionID,
		Status:            status,
		FinalMessage:      st.final,
		TotalCostUSD:      st.budget.TotalCostUSD(),
		TotalInputTokens:  st.inTokens,
		TotalOutputTokens: st.outTokens,
		FinalAnswerReason: reason,
	}, loopErr
}

// loop is turn-algorithm.md steps 16-18: run turns until one of the five
// termination conditions fires, routing the three graceful ones through
// the limit-reached final-answer turn.
//
// Done is checked before the bounds and the two trip detectors,
// deliberately. The algorithm's step 18 ("loop to step 1, unless a
// termination condition fired in 16/17 or DoneCheck") states no precedence
// among them, and checking Done first is the only ordering that doesn't
// spend a whole extra model call synthesizing a "final answer" for a model
// that already produced one — and doesn't then persist error_max_turns for
// a session whose model genuinely finished on its last permitted turn.
func (r *Runner) loop(ctx context.Context, st *run) (sessionv1.SessionStatus, string, error) {
	for {
		result, err := r.runTurn(ctx, st, r.request(st, false, ""))
		if err != nil {
			return r.turnFailure(ctx, st, err)
		}
		st.absorb(result)
		st.doom.Observe(result.CallHashes)

		if result.Done {
			r.logger.DebugContext(ctx, "session: done", "session_id", st.sessionID, "reason", result.DoneReason.String())
			return sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, "", nil
		}
		if st.doom.Tripped() {
			r.telem.Instruments().DoomLoops.Add(ctx, 1)
			return r.limitReached(ctx, st, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, ReasonDoomLoop)
		}
		if fired := st.budget.Check(r.clock().Sub(st.startedAt)); fired != bounds.FiredNone {
			reason := boundReason(fired)
			r.telem.Instruments().BoundsFired.Add(ctx, 1, metric.WithAttributes(telemetry.BoundKey.String(reason)))
			return r.limitReached(ctx, st, fired.Status(), reason)
		}
		if len(result.TrippedProviders) > 0 {
			r.logger.WarnContext(ctx, "session: circuit breaker tripped",
				"session_id", st.sessionID, "providers", result.TrippedProviders)
			return r.limitReached(ctx, st, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, ReasonCircuitBreaker)
		}
		st.turnIndex++
	}
}

// limitReached is turn-algorithm.md#limit-reached-behavior: EXACTLY one
// more turn with tool specs withheld and a synthetic instruction naming
// what fired, then the session ends with status. This is a hard limit —
// no soft "pause and let the user continue" mode is offered, so the
// spec's "if offered, the default MUST remain hard" is satisfied
// vacuously.
//
// status is bounds.Fired.Status() for a real bound. A doom loop and a
// circuit-breaker trip have no SessionStatus of their own; both are
// mapped to SESSION_STATUS_COMPLETED with the reason carried on
// Result.FinalAnswerReason — see this package's CLAUDE.md for why that
// beats reusing one of the three error_max_* subtypes or FAILED.
func (r *Runner) limitReached(ctx context.Context, st *run, status sessionv1.SessionStatus, reason string) (sessionv1.SessionStatus, string, error) {
	r.logger.InfoContext(ctx, "session: limit reached, running final-answer turn",
		"session_id", st.sessionID, "reason", reason, "status", status.String())

	st.turnIndex++
	result, err := r.runTurn(ctx, st, r.request(st, true, reason))
	if err != nil {
		failStatus, _, failErr := r.turnFailure(ctx, st, err)
		return failStatus, reason, failErr
	}
	st.absorb(result)
	return status, reason, nil
}

// turnFailure classifies a RunTurn error. A canceled caller context is
// normal control flow, not a failure: it is logged at INFO and reported as
// SESSION_STATUS_CANCELLED. Anything else is a genuine turn failure.
func (r *Runner) turnFailure(ctx context.Context, st *run, err error) (sessionv1.SessionStatus, string, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		r.logger.InfoContext(ctx, "session: canceled", "session_id", st.sessionID)
		return sessionv1.SessionStatus_SESSION_STATUS_CANCELLED, "", ctxErr
	}
	return sessionv1.SessionStatus_SESSION_STATUS_FAILED, "", fmt.Errorf("session: turn %d: %w", st.turnIndex, err)
}

// runTurn calls the turn driver and records the two turn-level metrics.
// The turn span itself is opened by internal/turn, under the session span
// this package already put on ctx.
func (r *Runner) runTurn(ctx context.Context, st *run, req turn.Request) (turn.Result, error) {
	r.logger.DebugContext(ctx, "session: running turn",
		"session_id", st.sessionID, "turn_index", req.TurnIndex, "final_answer", req.FinalAnswer)

	began := r.clock()
	result, err := r.turn.RunTurn(ctx, req)

	r.telem.Instruments().Turns.Add(ctx, 1)
	r.telem.Instruments().TurnDuration.Record(ctx, r.clock().Sub(began).Seconds())
	return result, err
}

// request builds one turn's inputs from the session's current state.
// FilesTouched stays nil: nothing in this build tracks the paths a session
// has touched, and inventing a partial list would be worse than the honest
// empty one every context provider already handles.
func (r *Runner) request(st *run, finalAnswer bool, reason string) turn.Request {
	return turn.Request{
		SessionID:               st.sessionID,
		TurnID:                  statebackend.NewEventID(r.clock()),
		TurnIndex:               st.turnIndex,
		WorkingDirectory:        st.spec.WorkingDirectory,
		Model:                   st.res.model,
		ModelTarget:             st.res.target,
		History:                 st.history,
		ScopedTools:             st.res.tools,
		AssembledTokensLastTurn: st.assembled,
		PlanMode:                st.spec.PlanMode,
		FinalAnswer:             finalAnswer,
		FinalAnswerReason:       reason,
	}
}

// absorb folds one turn's result into the session's running state:
// history and assembled-token carry-forward for the next turn, the
// aggregate token/cost figures Result reports, and the two budget updates
// turn-algorithm.md step 17 checks against.
//
// The Debit call is this package's job and not internal/turn's:
// internal/modelcall persists the cost_ledger row at usage-event time but
// holds no bounds.Tracker, and internal/turn holds none either, so the
// session driver is the only thing on the path that can decrement the
// live budget. turn.Result.CostUSD is this one turn's completion cost, not
// a running total, so it is debited exactly once here.
func (st *run) absorb(result turn.Result) {
	st.history = result.History
	st.assembled = result.AssembledTokens
	if result.Message != nil {
		st.final = result.Message
	}
	st.inTokens += result.Usage.GetInputTokens()
	st.outTokens += result.Usage.GetOutputTokens()

	// Only overwrite what this turn actually reported. A vendor that
	// publishes budgets on some responses and not others would otherwise
	// blank the operator's meter on every quiet turn.
	if limits := result.Usage.GetRateLimits(); len(limits) > 0 {
		st.quotas = limits
	}
	if vc := result.Usage.GetVendorCost(); vc != nil {
		st.vendorCost = vc
	}
	if m := result.ActualModel; m != "" {
		st.actualModel = m
	}

	st.budget.ObserveTurn()
	st.budget.Debit(result.CostUSD)
}

// boundReason names a fired bound for turn.Request.FinalAnswerReason.
func boundReason(fired bounds.Fired) string {
	switch fired {
	case bounds.FiredMaxTurns:
		return ReasonMaxTurns
	case bounds.FiredMaxCostUSD:
		return ReasonMaxCostUSD
	case bounds.FiredMaxWallClock:
		return ReasonMaxWallClock
	case bounds.FiredNone:
		return ""
	default:
		return ""
	}
}

// finalize persists the terminal status and dispatches session-end, in
// that order: a session-end subscriber still holds its callback grant and
// may read this session back through GetSession, and showing it "running"
// while its own end hook is firing would be a lie the state backend has no
// reason to tell.
func (r *Runner) finalize(ctx context.Context, st *run, sess *statebackend.Session, status sessionv1.SessionStatus, reason string) {
	endedAt := r.clock()
	if err := sess.SetStatus(ctx, status, &endedAt); err != nil {
		r.logger.ErrorContext(ctx, "session: persisting terminal status failed",
			"session_id", st.sessionID, "status", status.String(), "err", err)
	}

	r.dispatchSessionEnd(ctx, st, status)

	r.telem.Instruments().SessionsEnded.Add(ctx, 1,
		metric.WithAttributes(telemetry.SessionStatusKey.String(status.String())))
	r.logger.InfoContext(ctx, "session: ended",
		"session_id", st.sessionID,
		"status", status.String(),
		"reason", reason,
		"turns", st.turnIndex+1,
		"cost_usd", st.budget.TotalCostUSD())
}

// teardown unregisters and closes the live session, then releases every
// callback grant. It runs from Run's defer, so it is reached on every exit
// path including a panic — a leaked grant would leave a plugin able to
// call back naming a session that no longer exists.
func (r *Runner) teardown(sessionID string, live *sessionstate.Live, releases []func()) {
	r.sessions.Remove(sessionID)
	if err := live.Close(); err != nil {
		r.logger.Error("session: closing live session failed", "session_id", sessionID, "err", err)
	}
	for _, release := range releases {
		release()
	}
}

// dispatchSessionStart fires the session-start hook point exactly once.
// ParentSessionId is left unset: this build is root-sessions-only.
//
// A dispatch error is logged and swallowed rather than failing the
// session. session-start is neither veto-bearing nor transform-mutable, so
// there is no verdict and no payload edit to lose; failing an otherwise
// healthy session because a subscriber chain misbehaved would trade a
// working session for nothing.
func (r *Runner) dispatchSessionStart(ctx context.Context, st *run) {
	payload := &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_SessionStart{
			SessionStart: &hookv1.SessionStartPayload{
				SessionId:        st.sessionID,
				Profile:          st.res.profileName,
				WorkingDirectory: st.spec.WorkingDirectory,
			},
		},
	}
	if _, err := r.hooks.Dispatch(ctx, payload); err != nil {
		r.logger.Warn("session: session-start dispatch failed", "session_id", st.sessionID, "err", err)
	}
}

// dispatchSessionEnd fires the session-end hook point exactly once, with
// the terminal status. Same swallow-and-log contract as
// dispatchSessionStart, and for the same reason.
func (r *Runner) dispatchSessionEnd(ctx context.Context, st *run, status sessionv1.SessionStatus) {
	payload := &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_SessionEnd{
			SessionEnd: &hookv1.SessionEndPayload{
				SessionId: st.sessionID,
				Status:    status,
			},
		},
	}
	if _, err := r.hooks.Dispatch(ctx, payload); err != nil {
		r.logger.Warn("session: session-end dispatch failed", "session_id", st.sessionID, "err", err)
	}
}

// userMessage builds a session's initial history entry: the prompt string,
// and nothing else (subagents.md#context-isolation-default-fresh). The id
// is minted here because determinism.md makes every message id
// kernel-assigned; this message is not itself persisted, since
// state-backend.md's cost_ledger pairing means the only kernel path that
// writes a message event also writes a cost row, and a user prompt has no
// cost — see this package's CLAUDE.md.
func userMessage(now time.Time, prompt string) *contentv1.Message {
	return &contentv1.Message{
		Role: contentv1.Role_ROLE_USER,
		Id:   statebackend.NewEventID(now),
		Content: []*contentv1.ContentBlock{{
			Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: prompt}},
		}},
	}
}
