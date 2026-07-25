package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pluggableharness/agent/internal/circuitbreaker"
	"github.com/pluggableharness/agent/internal/contextassembly"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/interactive/drivers/unattended"
	"github.com/pluggableharness/agent/internal/modelcall"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/autoallow"
	"github.com/pluggableharness/agent/internal/plangate"
	"github.com/pluggableharness/agent/internal/retrypolicy"
	"github.com/pluggableharness/agent/internal/session"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/tooldispatch"
	"github.com/pluggableharness/agent/internal/turn"
)

// Circuit-breaker thresholds, and why these numbers.
//
// plan-apply-gate.md#circuit-breaker-on-repeated-denials specifies the
// mechanism ("N consecutive deny decisions, or M denials within a sliding
// window") but deliberately names neither N nor M, and it is a SHOULD
// rather than a MUST. These are therefore a project judgment call in the
// same spirit as internal/config's DefaultHookTimeoutMS/DefaultToolTimeoutMS
// — documented here, changeable in one place, not fabricated spec.
const (
	// breakerConsecutiveThreshold is the shortest run of back-to-back
	// denials that is unambiguously a denial storm rather than a model
	// legitimately exploring adjacent calls after being told no once.
	breakerConsecutiveThreshold = 3

	// breakerWindowSize / breakerWindowThreshold catch the oscillating
	// case the consecutive counter misses: a model that alternates
	// between two denied calls and one allowed one never reaches three in
	// a row, but is still looping against a wall. Eight denials inside
	// twenty decisions is well past any plausible healthy session.
	breakerWindowSize      = 20
	breakerWindowThreshold = 8
)

// sessionMaxRetries is the session-wide model-retry cap
// error-recovery.md#model-provider-errors requires be tracked separately
// from the per-attempt chain cap (settings.retry.max_retries, default 5).
//
// No spec value exists, so: twenty allows four fully-exhausted attempt
// chains across a whole session before the kernel stops paying for a
// provider that is evidently down, while still absorbing the isolated
// rate-limit or 5xx that a healthy long session hits.
const sessionMaxRetries = 20

// ErrNoLiveSession reports an append against a sessionSink that no session
// has been bound to yet. See sessionSink for when that is possible.
var ErrNoLiveSession = errors.New("kernel: event sink has no live session bound")

// sessionSink is the late-binding seam between the turn-stack
// collaborators and the session file they persist into.
//
// It exists to resolve a genuine ordering problem, not as indirection for
// its own sake — the same shape, and the same reason, as
// internal/pluginhost's callbackSlot. Five packages
// (internal/contextassembly, internal/modelcall, internal/tooldispatch,
// internal/hookdispatch, internal/plangate) each declare their event sink
// as *statebackend.Session's own Append* signatures, so each needs the
// open session handle at construction. But internal/session mints the
// session id and creates that file itself, inside Runner.Run — which is
// called with an already-constructed turn driver. Nothing above
// internal/session can therefore hold the handle before the session
// exists.
//
// newTurnStack binds the sink on the first RunTurn call, resolving the
// handle out of the live-session Table internal/session has already
// registered it in. An append before that returns ErrNoLiveSession rather
// than panicking on a nil handle; see this package's CLAUDE.md for the one
// window in which that is reachable.
//
// It binds a *sessionstate.Live, NOT the raw *statebackend.Session that
// Live wraps, and that distinction is load-bearing rather than incidental.
// Live's own Append* methods are what serialize a session's writes under
// one lock and republish each committed event onto the reserved
// kernel.event.{kind} bus topic (event-bus.md#the-kernel-namespace).
// Binding the raw handle would persist every kernel-originated event
// correctly and publish none of them — a plugin subscribed to
// kernel.event.* would see other plugins' Emit calls and never a message,
// tool_call, tool_result, plan, or apply. Don't reach past Live here.
type sessionSink struct {
	inner atomic.Pointer[sessionstate.Live]
}

// bind installs live as the target every subsequent append forwards to.
func (s *sessionSink) bind(live *sessionstate.Live) { s.inner.Store(live) }

// AppendEvent forwards to the bound session.
func (s *sessionSink) AppendEvent(ctx context.Context, ev statebackend.Event) (int64, error) {
	live := s.inner.Load()
	if live == nil {
		return 0, ErrNoLiveSession
	}
	return live.AppendEvent(ctx, ev)
}

// AppendMessage forwards to the bound session.
func (s *sessionSink) AppendMessage(ctx context.Context, ev statebackend.Event, cost statebackend.CostEntry) (int64, error) {
	live := s.inner.Load()
	if live == nil {
		return 0, ErrNoLiveSession
	}
	return live.AppendMessage(ctx, ev, cost)
}

// AppendPlan forwards to the bound session.
func (s *sessionSink) AppendPlan(ctx context.Context, ev statebackend.Event, items []statebackend.PlanItem) (int64, error) {
	live := s.inner.Load()
	if live == nil {
		return 0, ErrNoLiveSession
	}
	return live.AppendPlan(ctx, ev, items)
}

// The sink stands in for *statebackend.Session at five call sites, each of
// which declares its own narrow interface. Anchor all of them, so a drift
// in any one of those interfaces fails to build here rather than at a
// wiring line buried in newTurnStack.
var (
	_ contextassembly.EventSink = (*sessionSink)(nil)
	_ hookdispatch.EventSink    = (*sessionSink)(nil)
	_ modelcall.MessageSink     = (*sessionSink)(nil)
	_ tooldispatch.EventSink    = (*sessionSink)(nil)
	_ plangate.PlanSink         = (*sessionSink)(nil)
)

// turnStack is the session.TurnDriver internal/session is constructed
// over: a lazily-built *turn.Driver plus everything under it that is
// scoped to one session rather than to the process.
//
// The laziness is not an optimization. internal/plangate.Config requires
// the session id at construction and one *circuitbreaker.Breaker is scoped
// to one session (shared by the gate and the tool scheduler, per
// internal/session's CLAUDE.md) — but the session id is minted inside
// Runner.Run, which already holds this driver. turn.Request.SessionID is
// the first place the id is visible from up here, so that is where the
// per-session half of the stack gets built.
type turnStack struct {
	k *kernel

	mu        sync.Mutex
	sessionID string
	driver    session.TurnDriver
}

// newTurnStack returns a turnStack over k's process-wide collaborators.
func newTurnStack(k *kernel) *turnStack { return &turnStack{k: k} }

// RunTurn builds this session's turn driver on first call and delegates
// every turn to it.
func (t *turnStack) RunTurn(ctx context.Context, req turn.Request) (turn.Result, error) {
	driver, err := t.driverFor(ctx, req.SessionID)
	if err != nil {
		return turn.Result{}, err
	}
	return driver.RunTurn(ctx, req)
}

// driverFor returns the driver for sessionID, building it once.
//
// A second, different session id is refused rather than served: this build
// runs exactly one root session per process (there is no RunSession
// callback and no sub-agent spawning), so a second id means the wiring
// assumption above has silently stopped holding, and quietly rebuilding
// the stack would rebind the shared sink out from under the first session.
func (t *turnStack) driverFor(ctx context.Context, sessionID string) (session.TurnDriver, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.driver != nil {
		if t.sessionID != sessionID {
			return nil, fmt.Errorf("kernel: turn stack is bound to session %s and cannot also serve %s: this build runs one root session per process", t.sessionID, sessionID)
		}
		return t.driver, nil
	}

	driver, err := t.k.newTurnDriver(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	t.sessionID, t.driver = sessionID, driver
	return driver, nil
}

// newTurnDriver builds the per-session half of the turn stack and binds
// the event sink to that session's open handle.
//
// The contextcheck suppressions below are the same false positive
// openStores documents: every constructor here takes no context by
// design, and the linter reaches them only through a nil-telemetry
// fallback branch that a live k.telem makes unreachable. ctx is still
// threaded through everything that genuinely takes one.
func (k *kernel) newTurnDriver(ctx context.Context, sessionID string) (session.TurnDriver, error) {
	live, ok := k.sessions.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("kernel: session %s is not in the live-session table", sessionID)
	}
	k.sink.bind(live)

	// One Breaker per session, wired into BOTH the plan gate (which
	// records denials) and the tool scheduler (which records crashes).
	// internal/session deliberately has no Breaker field — both consumers
	// sit below its TurnDriver seam, so this is the only place the shared
	// instance can be created. See internal/session/CLAUDE.md.
	breaker := circuitbreaker.New(circuitbreaker.Config{
		ConsecutiveThreshold: breakerConsecutiveThreshold,
		WindowSize:           breakerWindowSize,
		WindowThreshold:      breakerWindowThreshold,
	})

	assembler := contextassembly.New(contextassembly.Config{
		Tokens:    k.tokens,
		Events:    k.sink,
		Telemetry: k.telem,
		Logger:    k.logger,
	})

	caller := modelcall.New(modelcall.Config{
		Retry:     retrypolicy.FromConfig(k.cfg.Settings.Retry, sessionMaxRetries),
		Events:    k.sink,
		Telemetry: k.telem,
		Logger:    k.logger,
	})

	scheduler := tooldispatch.New(tooldispatch.Config{ //nolint:contextcheck // see newTurnDriver's note
		// TRACKED DEVIATION: no frontend exists to ask a human anything,
		// so every interactive-kind call is refused rather than answered
		// with a fabricated response. See
		// internal/interactive/drivers/unattended's package doc for why
		// this one needs no acknowledgment flag while its autoallow
		// sibling below does.
		Interactive:    unattended.New(k.logger, k.telem),
		Breaker:        breaker,
		Events:         k.sink,
		DefaultTimeout: toolTimeout(k.cfg.Settings.DefaultToolTimeoutMS),
		Telemetry:      k.telem,
		Logger:         k.logger,
	})

	// ------------------------------------------------------------------
	// TRACKED DEVIATION FROM A SPEC MUST — READ BEFORE CHANGING.
	//
	// plan-apply-gate.md#decision-semantics requires an `ask` decision to
	// emit a permission-request event and BLOCK that plan item until a
	// frontend returns a human's verdict. This kernel has no frontend
	// attach path, so it cannot satisfy that MUST. Until one exists,
	// every `ask` item is auto-approved by the operator-approved stand-in
	// below, and the acknowledgment is spelled out at this call site
	// precisely so no code review can miss it.
	//
	// Consequence, in plain terms: a session run by this build executes
	// mutating tool calls that a human was supposed to approve, and its
	// plan_items.decided_by audit rows say exactly that, per item.
	// autoallow.New logs one WARN at construction and one per resolution.
	//
	// The fix is not to soften anything here: it is the real
	// internal/plandecision/drivers/frontend resolver, which stops this
	// driver being the default the moment it lands.
	// ------------------------------------------------------------------
	resolver, err := autoallow.New(autoallow.Config{ //nolint:contextcheck // see newTurnDriver's note
		AcknowledgeUnsafeAutoAllow: true,
		Logger:                     k.logger,
		Telemetry:                  k.telem,
	})
	if err != nil {
		return nil, fmt.Errorf("kernel: plan-decision resolver: %w", err)
	}
	k.logger.WarnContext(ctx, "kernel: UNSAFE plan-decision resolver active: every ask-decision plan item will be auto-approved with no human in the loop",
		"session_id", sessionID,
		"decided_by", autoallow.DecidedBy,
		"reason", "no frontend attach path exists in this build")

	gate := plangate.New(plangate.Config{ //nolint:contextcheck // see newTurnDriver's note
		SessionID: sessionID,
		Rules:     k.cfg.Policies,
		// GateHooks is internal/turn's own adapter from this Dispatcher
		// to the narrower HookDispatcher internal/plangate declares for
		// itself. Do not write a second one — see internal/turn's
		// CLAUDE.md on why plangate keeps its own types.
		Hooks:    turn.GateHooks{Dispatcher: k.hooks},
		Resolver: resolver,
		Breaker:  breaker,
		Events:   k.sink,
		Tools:    k.catalog,
	}, plangate.WithTelemetry(k.telem), plangate.WithLogger(k.logger))

	driver, err := turn.New(turn.Config{ //nolint:contextcheck // see newTurnDriver's note
		Hooks:     k.hooks,
		Context:   assembler,
		Model:     caller,
		Gate:      gate,
		Tools:     scheduler,
		Catalog:   k.catalog,
		Telemetry: k.telem,
		Logger:    k.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("kernel: turn driver: %w", err)
	}

	k.logger.DebugContext(ctx, "kernel: turn stack built", "session_id", sessionID)
	return driver, nil
}
