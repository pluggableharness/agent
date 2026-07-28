package session

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
)

// TopicState is the event-bus topic for SessionState republish.
const TopicState = "kernel.state"

// ErrHandleClosed reports use of a Handle after Close.
var ErrHandleClosed = errors.New("session: handle closed")

// ErrSessionBusy reports Submit while another Submit is in flight.
var ErrSessionBusy = errors.New("session: session is busy")

// Handle is one long-lived interactive session: Open then zero or more
// Submit calls. Unlike Runner.Run, Handle does not complete the session
// when the model finishes a response — the session stays RUNNING for the
// next operator input until Close or a bound/cancel terminates it.
type Handle struct {
	r        *Runner
	st       *run
	sess     *statebackend.Session
	live     *sessionstate.Live
	releases []func()

	mu     sync.Mutex
	closed bool
	busy   bool
	cancel context.CancelFunc
}

// Open creates a session file, registers the live session, and returns a
// Handle without running any turns. Spec.Prompt, when non-empty, seeds
// history as the first user message but does not start a turn.
func (r *Runner) Open(ctx context.Context, spec Spec) (*Handle, error) {
	res, err := r.resolve(spec)
	if err != nil {
		return nil, err
	}
	startedAt := r.clock()
	sessionID := statebackend.NewSessionID(startedAt)

	sess, err := r.store.Create(ctx, statebackend.SessionMeta{
		SessionID: sessionID,
		Profile:   res.profileName,
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: startedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("session: open: create: %w", err)
	}

	live := sessionstate.NewLive(sess, r.bus, res.limits, nil, r.clock, r.telem, r.logger) //nolint:contextcheck
	r.sessions.Put(sessionID, live)

	releases := make([]func(), 0, len(res.keys))
	for _, key := range res.keys {
		releases = append(releases, r.scopes.Grant(key, sessionID))
	}

	history := []*contentv1.Message{}
	if spec.Prompt != "" {
		history = append(history, userMessage(r.clock(), spec.Prompt))
	}

	st := &run{
		sessionID: sessionID,
		spec:      spec,
		res:       res,
		budget:    live.Budget(),
		doom:      r.newDetector(),
		startedAt: startedAt,
		history:   history,
	}

	h := &Handle{r: r, st: st, sess: sess, live: live, releases: releases}
	r.logger.InfoContext(ctx, "session: opened",
		"session_id", sessionID,
		"profile", res.profileName,
		"model_id", res.model.Ref.ID)
	r.dispatchSessionStart(ctx, st)
	_ = h.publishState(ctx)
	return h, nil
}

// SessionID returns this handle's session id.
func (h *Handle) SessionID() string { return h.st.sessionID }

// WorkingDirectory returns the session's working directory.
func (h *Handle) WorkingDirectory() string { return h.st.spec.WorkingDirectory }

// Submit appends operator content as a user message and runs turns until
// the model finishes (Done), a bound/doom/breaker fires, or ctx is
// canceled. Returns the first turn id of this submission.
func (h *Handle) Submit(ctx context.Context, content []*contentv1.ContentBlock) (turnID string, err error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return "", ErrHandleClosed
	}
	if h.busy {
		h.mu.Unlock()
		return "", ErrSessionBusy
	}
	h.busy = true
	runCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		h.busy = false
		h.cancel = nil
		h.mu.Unlock()
		cancel()
	}()

	if len(content) == 0 {
		return "", fmt.Errorf("session: submit: content is required")
	}
	msg := &contentv1.Message{
		Id:      statebackend.NewEventID(h.r.clock()),
		Role:    contentv1.Role_ROLE_USER,
		Content: content,
	}
	h.st.history = append(h.st.history, msg)

	firstReq := h.r.request(h.st, false, "")
	turnID = firstReq.TurnID

	for {
		req := h.r.request(h.st, false, "")
		if turnID == "" {
			turnID = req.TurnID
		}
		result, turnErr := h.r.runTurn(runCtx, h.st, req)
		if turnErr != nil {
			status, _, failErr := h.r.turnFailure(runCtx, h.st, turnErr)
			_ = h.markTerminal(context.WithoutCancel(runCtx), status)
			return turnID, failErr
		}
		h.st.absorb(result)
		h.st.doom.Observe(result.CallHashes)
		_ = h.publishState(runCtx)

		if result.Done {
			return turnID, nil
		}
		if h.st.doom.Tripped() {
			h.r.telem.Instruments().DoomLoops.Add(runCtx, 1)
			status, _, lerr := h.r.limitReached(runCtx, h.st, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, ReasonDoomLoop)
			_ = h.markTerminal(context.WithoutCancel(runCtx), status)
			return turnID, lerr
		}
		if fired := h.st.budget.Check(h.r.clock().Sub(h.st.startedAt)); fired != bounds.FiredNone {
			reason := boundReason(fired)
			status, _, lerr := h.r.limitReached(runCtx, h.st, fired.Status(), reason)
			_ = h.markTerminal(context.WithoutCancel(runCtx), status)
			return turnID, lerr
		}
		if len(result.TrippedProviders) > 0 {
			status, _, lerr := h.r.limitReached(runCtx, h.st, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, ReasonCircuitBreaker)
			_ = h.markTerminal(context.WithoutCancel(runCtx), status)
			return turnID, lerr
		}
		h.st.turnIndex++
	}
}

// Interrupt cancels the in-flight Submit, if any.
func (h *Handle) Interrupt() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
}

// Close finalizes the session as COMPLETED (if still RUNNING) and releases
// grants. Safe to call once.
func (h *Handle) Close(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	if h.cancel != nil {
		h.cancel()
	}
	h.mu.Unlock()

	finCtx := context.WithoutCancel(ctx)
	meta, err := h.sess.Meta(finCtx)
	if err == nil && meta.Status == sessionv1.SessionStatus_SESSION_STATUS_RUNNING {
		_ = h.markTerminal(finCtx, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED)
	}
	h.r.dispatchSessionEnd(finCtx, h.st, metaStatusOr(meta, sessionv1.SessionStatus_SESSION_STATUS_COMPLETED))
	// teardown deliberately takes no context: statebackend.Session.Close
	// derives its own, precisely so a canceled caller context cannot prevent
	// a session file from being checkpointed and closed. Same rationale as
	// Runner.Run's own deferred teardown.
	h.r.teardown(h.st.sessionID, h.live, h.releases) //nolint:contextcheck // see comment above
	return nil
}

func metaStatusOr(meta statebackend.SessionMeta, fallback sessionv1.SessionStatus) sessionv1.SessionStatus {
	if meta.Status != sessionv1.SessionStatus_SESSION_STATUS_UNSPECIFIED {
		return meta.Status
	}
	return fallback
}

// State builds the fixed-schema SessionState snapshot.
func (h *Handle) State(ctx context.Context) (*sessionv1.SessionState, error) {
	meta, err := h.sess.Meta(ctx)
	if err != nil {
		return nil, err
	}
	info := &sessionv1.SessionInfo{
		SessionId: meta.SessionID,
		Profile:   meta.Profile,
		Status:    meta.Status,
		Depth:     int32(meta.Depth), // #nosec G115
		StartedAt: timestamppb.New(meta.StartedAt),
	}
	if meta.ParentSessionID != "" {
		info.ParentSessionId = &meta.ParentSessionID
	}
	if meta.EndedAt != nil {
		info.EndedAt = timestamppb.New(*meta.EndedAt)
	}
	if cost := h.st.budget.TotalCostUSD(); cost != 0 {
		info.CostUsd = &cost
	}

	elapsed := h.r.clock().Sub(h.st.startedAt)
	if meta.EndedAt != nil {
		elapsed = meta.EndedAt.Sub(meta.StartedAt)
	}

	state := &sessionv1.SessionState{
		Info:             info,
		WorkingDirectory: h.st.spec.WorkingDirectory,
		TurnCount:        int32(h.st.turnIndex + 1), // #nosec G115 — turns completed-ish
		Elapsed:          durationpb.New(elapsed),
		TotalTokens:      h.st.inTokens + h.st.outTokens,
	}
	if h.st.res.model.Ref.ID != "" {
		state.Model = &sessionv1.ModelState{
			Id:       h.st.res.model.Ref.ID,
			Provider: h.st.res.model.Ref.Provider,
		}
	}
	if h.st.res.target != nil && h.st.res.target.GetEffectiveCeiling() > 0 {
		state.Context = &sessionv1.ContextState{
			UsedTokens:   h.st.assembled,
			WindowTokens: h.st.res.target.GetEffectiveCeiling(),
		}
	}
	// Vendor-reported state from the most recent completion. Each is left
	// absent rather than zero-filled when the vendor said nothing —
	// "no reading" and "a reading of zero" are different facts to a
	// status bar, and conflating them is how a usage meter starts lying.
	state.Quotas = h.st.quotas
	state.VendorCost = h.st.vendorCost
	if h.st.actualModel != "" {
		state.ActualModel = &h.st.actualModel
	}
	if wd := h.st.spec.WorkingDirectory; wd != "" {
		if vcs := probeVCS(ctx, wd); vcs != nil {
			state.Vcs = vcs
		}
	}
	return state, nil
}

// Info returns SessionInfo for lifecycle RPCs.
func (h *Handle) Info(ctx context.Context) (*sessionv1.SessionInfo, error) {
	state, err := h.State(ctx)
	if err != nil {
		return nil, err
	}
	return state.GetInfo(), nil
}

func (h *Handle) publishState(ctx context.Context) error {
	state, err := h.State(ctx)
	if err != nil {
		return err
	}
	payload, err := proto.Marshal(state)
	if err != nil {
		return err
	}
	return h.r.bus.Publish(ctx, eventbus.Event{Topic: TopicState, Payload: payload})
}

func (h *Handle) markTerminal(ctx context.Context, status sessionv1.SessionStatus) error {
	now := h.r.clock()
	if err := h.sess.SetStatus(ctx, status, &now); err != nil {
		h.r.logger.ErrorContext(ctx, "session: set status", "err", err)
		return err
	}
	return h.publishState(ctx)
}

// probeVCS best-effort reads git status for SessionState.Vcs. Every call is
// context-bound: `git status --porcelain` on a large working tree is not
// instant, and a SessionState snapshot must not outlive the request that
// asked for it.
//
// The three commands are the literal "git"; only the -C path varies, and git
// treats it as a path argument, never a shell fragment.
func probeVCS(ctx context.Context, dir string) *sessionv1.VcsState {
	git := func(args ...string) ([]byte, error) {
		// Both suppressions are load-bearing and neither is redundant:
		// golangci-lint reads //nolint, while the standalone gosec the
		// security workflow runs reads only #nosec, and #nosec binds only
		// when it sits on the flagged line itself here — on a preceding
		// line it does not attach to a node inside this closure. Carrying
		// just the //nolint passed locally and failed CI.
		//nolint:gosec // G204: constant command, path-only variable argument (see doc comment)
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- constant "git"; only the -C path varies
		return cmd.Output()
	}

	branchOut, err := git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil
	}
	branch := strings.TrimSpace(string(branchOut))
	remoteOut, _ := git("remote", "get-url", "origin")
	remote := strings.TrimSpace(string(remoteOut))
	dirty := false
	if statusOut, err := git("status", "--porcelain"); err == nil {
		dirty = len(strings.TrimSpace(string(statusOut))) > 0
	}
	vcs := &sessionv1.VcsState{}
	if branch != "" {
		vcs.Branch = &branch
	}
	if remote != "" {
		vcs.Remote = &remote
	}
	vcs.Dirty = &dirty
	return vcs
}
