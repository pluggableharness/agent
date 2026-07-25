package plangate

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/circuitbreaker"
	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// defaultPreviewTimeout bounds one Preview RPC during plan construction.
// [docs/specifications/agent-loop/plan-apply-gate.md#preview-flow] requires
// the kernel not to block plan construction on a slow Preview "beyond its
// own ordinary per-RPC deadline" (.claude/rules/grpc.md's "Context and
// deadlines"), and to degrade a timeout to an absent preview rather than
// an aborted plan.
const defaultPreviewTimeout = 5 * time.Second

// planEventSchemaVersion versions the shape of the persisted plan and
// apply event payloads (pluggableharness.event.v1.PlanEvent and
// ApplyEvent). It tracks the event.v1 payload generation, exactly like
// statebackend's reserved kernel producer version — not the kernel
// binary's release.
const planEventSchemaVersion = "1"

// HookOutcome is one hook chain's result: the post-transform payload, the
// chain's coarse verdict, and the identity of whatever produced a non-ALLOW
// verdict.
//
// Its field set is deliberately identical to internal/hookdispatch's own
// Outcome, so the caller that owns both sides converts between them with a
// single Go struct conversion rather than a hand-written adapter — see
// doc.go for why this package still declares its own type instead of
// importing that one.
type HookOutcome struct {
	// Payload is the payload as the chain left it. Not read by this
	// package — no field of PlanReadyPayload is transform-mutable
	// (pluggableharness.hook.v1's own PlanReadyPayload comment) — but
	// carried so this interface matches the dispatcher's real shape.
	Payload *hookv1.HookPayload
	// Decision is the chain's verdict, ALLOW unless a veto subscriber
	// denied. HOOK_DECISION_DENY at HOOK_POINT_PLAN_READY denies the
	// entire plan (pluggableharness.hook.v1.HookDecision's own doc
	// comment: a veto subscriber at plan-ready returns only this coarse
	// ALLOW/DENY over the whole plan). Meaningful only when Dispatch
	// returned a nil error.
	Decision hookv1.HookDecision
	// DeniedBy identifies whoever produced a non-ALLOW Decision — a
	// plugin subscriber's agent.hcl local name, or a pinned kernel
	// veto's name. Empty when Decision is ALLOW. This is what the gate
	// writes into every denied item's decided_by as
	// "hook-veto:<provider>".
	DeniedBy string
}

// HookDispatcher is the narrow view of hook dispatch this package
// consumes: one call that runs a whole chain and reports its outcome. The
// hook point is not a parameter — HookPayload is a oneof, and the variant
// set on it IS the point.
//
// It is declared here, and NOT satisfied by importing internal/hookdispatch
// — see this package's doc.go for why that decoupling is deliberate and
// MUST survive internal/hookdispatch existing. A dispatcher implementation
// owns everything about chain construction and ordering, including the
// guarantee that the kernel-privileged policy veto is pinned ahead of
// every plugin subscriber at HOOK_POINT_PLAN_READY; this package calls
// Dispatch once and trusts that guarantee rather than re-deriving it.
//
// Two contract details this package relies on:
//
//   - A veto subscriber that errors or times out fails CLOSED, surfacing
//     as HOOK_DECISION_DENY with a nil error
//     ([docs/specifications/agent-loop/hook-dispatch.md]'s timeout
//     behavior). A denial therefore may be a failure rather than a
//     considered verdict; either way the plan is denied.
//   - A returned error is a dispatcher-level failure (a cancelled parent
//     context, a chain abort), NOT an implicit verdict. Decision MUST NOT
//     be read when err is non-nil, and this package does not treat such an
//     error as a deny — it propagates it.
type HookDispatcher interface {
	Dispatch(ctx context.Context, payload *hookv1.HookPayload) (HookOutcome, error)
}

// PlanSink is the narrow view of the session's sole-writer state backend
// this package needs: the two append paths that persist a turn's plan and
// its apply outcome. *statebackend.Session satisfies it as written.
//
// Both writes are single calls on purpose. AppendPlan puts the plan event
// and every plan_items row in ONE transaction
// ([docs/specifications/state-backend.md#plan_items]), which is why this
// package resolves every ask BEFORE persisting: a plan_items row only ever
// holds a made decision, so there is nowhere to park a PENDING or ASK row
// and revisit it later.
type PlanSink interface {
	AppendPlan(ctx context.Context, ev statebackend.Event, items []statebackend.PlanItem) (int64, error)
	AppendEvent(ctx context.Context, ev statebackend.Event) (int64, error)
}

// ToolResolver is the narrow view of internal/providercatalog.Catalog this
// package needs at decision time: the input schema an ask-resolving
// frontend's corrected_input MUST be re-validated against
// ([docs/specifications/frontend/frontend-protocol.md#plan_decisioncorrected_input]).
// providercatalog.Catalog satisfies it as written.
type ToolResolver interface {
	Tool(provider, tool string) (providercatalog.ToolHandle, error)
}

// Config is the Gate's required collaborators. Every field except Breaker
// and Tools is required; New panics on a missing one rather than
// returning an error, because a Gate constructed without a resolver or a
// sink cannot fail safely later — it would silently skip an ask or an
// audit row.
//
// This is a dependency struct, not the zero-value-means-default config
// struct .claude/rules/go-style.md warns against: genuinely optional
// settings are functional Options below.
type Config struct {
	// SessionID is the session this Gate is scoped to, for log and span
	// correlation. Unbounded — never a metric attribute.
	SessionID string
	// Rules is the operator's policy rule set, already validated at
	// config-load time by policy.ValidateRules.
	Rules []policy.Rule
	// Hooks dispatches the plan-ready chain.
	Hooks HookDispatcher
	// Resolver resolves PLAN_DECISION_ASK items to a terminal verdict.
	Resolver plandecision.Resolver
	// Breaker tracks repeated denials per provider
	// ([plan-apply-gate.md#circuit-breaker-on-repeated-denials]). May be
	// nil, in which case no trip is ever reported.
	Breaker *circuitbreaker.Breaker
	// Events persists the turn's plan and apply events.
	Events PlanSink
	// Tools resolves an operation's declared input schema for
	// corrected_input re-validation. May be nil, in which case a
	// resolver-supplied corrected_input is accepted without a schema
	// check — the honest behavior when no schema is known, matching
	// plandecision.ValidateDecision's own nil-InputSchema handling.
	Tools ToolResolver
}

// Option configures optional Gate behavior.
type Option func(*Gate)

// WithPreviewTimeout overrides the per-Preview-RPC deadline plan
// construction applies. A non-positive d is ignored.
func WithPreviewTimeout(d time.Duration) Option {
	return func(g *Gate) {
		if d > 0 {
			g.previewTimeout = d
		}
	}
}

// WithClock overrides the wall clock used for event timestamps and event
// ids. Timestamps are display-only and never ordering-authoritative
// (.claude/rules/determinism.md); this exists so a test can pin them.
func WithClock(clock func() time.Time) Option {
	return func(g *Gate) {
		if clock != nil {
			g.clock = clock
		}
	}
}

// WithLogger overrides the logger. Defaults to slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(g *Gate) {
		if logger != nil {
			g.logger = logger
		}
	}
}

// WithTelemetry overrides the telemetry provider. Defaults to a provider
// with every signal disabled.
func WithTelemetry(telem *telemetry.Provider) Option {
	return func(g *Gate) {
		if telem != nil {
			g.telem = telem
		}
	}
}

// sessionVerdict is one remembered PLAN_DECISION_SCOPE_SESSION verdict.
// Only the verdict is remembered, never a frozen copy of the operator's
// corrected_input: [plan-apply-gate.md#plandecisionscope-semantics] is
// explicit that a SESSION scope remembers the *verdict*, and that a
// corrected_input is re-validated against each future call's own arguments
// rather than replayed.
type sessionVerdict struct {
	decision  planv1.PlanDecision
	decidedBy string
}

// scopeKey is the (provider, operation_name) pair a SESSION-scoped verdict
// is remembered under, per [plan-apply-gate.md#plandecisionscope-semantics].
type scopeKey struct {
	provider  string
	operation string
}

// Gate is one session's plan/apply gate. Construct with New; the zero
// value is not usable.
//
// Safe for concurrent use: a turn may build and decide plans from more
// than one goroutine, and the SESSION-scope map is shared across every
// turn in the session.
type Gate struct {
	sessionID string
	rules     []policy.Rule
	hooks     HookDispatcher
	resolver  plandecision.Resolver
	breaker   *circuitbreaker.Breaker
	events    PlanSink
	tools     ToolResolver

	previewTimeout time.Duration
	clock          func() time.Time
	logger         *slog.Logger
	telem          *telemetry.Provider

	// mu guards scoped, the in-memory PLAN_DECISION_SCOPE_SESSION map.
	// It lives on the Gate — and therefore lapses when the Gate does —
	// which is the whole of this build's SESSION-scope expiry policy.
	mu     sync.Mutex
	scoped map[scopeKey]sessionVerdict
}

// New returns a Gate for one session. It panics if cfg omits Hooks,
// Resolver, or Events — see Config.
func New(cfg Config, opts ...Option) *Gate {
	switch {
	case cfg.Hooks == nil:
		panic("plangate: New: Config.Hooks is required")
	case cfg.Resolver == nil:
		panic("plangate: New: Config.Resolver is required")
	case cfg.Events == nil:
		panic("plangate: New: Config.Events is required")
	}

	g := &Gate{
		sessionID:      cfg.SessionID,
		rules:          cfg.Rules,
		hooks:          cfg.Hooks,
		resolver:       cfg.Resolver,
		breaker:        cfg.Breaker,
		events:         cfg.Events,
		tools:          cfg.Tools,
		previewTimeout: defaultPreviewTimeout,
		clock:          time.Now,
		logger:         slog.Default(),
		telem:          defaultTelemetry(),
		scoped:         make(map[scopeKey]sessionVerdict),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// defaultTelemetry builds the every-signal-disabled Provider a Gate falls
// back to, matching the fallback internal/sessionstate and
// internal/statebackend already use so a caller that doesn't care about
// telemetry needn't construct a Provider to satisfy this constructor.
func defaultTelemetry() *telemetry.Provider {
	prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		// Unreachable: telemetry.Config{}'s zero value is fixed and valid,
		// the same reasoning internal/eventbus.New and
		// internal/sessionstate.NewLive give for panicking here rather
		// than threading an error through an otherwise infallible
		// constructor.
		panic(err)
	}
	return prov
}

// recallScope returns the remembered SESSION-scope verdict for
// (provider, operation), if any.
func (g *Gate) recallScope(provider, operation string) (sessionVerdict, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.scoped[scopeKey{provider: provider, operation: operation}]
	return v, ok
}

// rememberScope records a SESSION-scope verdict for (provider, operation)
// for the remainder of this Gate's — that is, this session's — lifetime.
func (g *Gate) rememberScope(provider, operation string, v sessionVerdict) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scoped[scopeKey{provider: provider, operation: operation}] = v
}

// recordDenial debits provider's circuit breaker and reports whether that
// denial tripped it. A nil Breaker never trips
// ([plan-apply-gate.md#circuit-breaker-on-repeated-denials] is a SHOULD,
// so a build without one is conformant).
func (g *Gate) recordDenial(provider string) bool {
	if g.breaker == nil {
		return false
	}
	return g.breaker.RecordDenial(provider)
}

// denialError synthesizes the ToolError that accompanies a denied call.
// Policy denial is a permission decision, so the category is
// PERMISSION_DENIED and it is never retryable — re-issuing the identical
// call would hit the identical rule, which is exactly the denial-storm the
// circuit breaker exists to interrupt.
func denialError(reason string) *toolv1.ToolError {
	return &toolv1.ToolError{
		Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED,
		Message:   reason,
		Retryable: false,
	}
}

// Errors this package returns. Every one is a caller/programming error or
// an unavailable capability, never a routine decision outcome: an
// allow/deny verdict is data on the returned value, not an error.
var (
	// ErrNoTurnID is returned when a BuildRequest carries no turn id.
	ErrNoTurnID = errors.New("plangate: build request has no turn id")

	// ErrNilItem is returned when a ProvisionalItem, a Plan, or a plan's
	// item slot carries no PlanItem.
	ErrNilItem = errors.New("plangate: no plan item")

	// ErrPreviewNotAllowed is returned when a non-resource provisional
	// item arrives with preview already populated.
	// [plan-apply-gate.md#preview-flow] makes this a MUST NOT: preview is
	// a resource-item concept, mirroring how only resource items reach
	// the allow/ask/deny decision at all. Build enforces it rather than
	// trusting the caller.
	ErrPreviewNotAllowed = errors.New("plangate: preview is populated on a non-resource plan item")

	// ErrNonTerminalDecision is returned when an item would be persisted
	// with a decision that is not ALLOW or DENY. Reaching it is a bug in
	// this package, not a caller error — it is asserted before the
	// AppendPlan write so a non-terminal row can never be written.
	ErrNonTerminalDecision = errors.New("plangate: plan item decision is not terminal")

	// ErrMissingOutcome is returned by Result when an allowed plan item
	// has no matching ApplyOutcome. plan.v1.ApplyResult carries "one
	// outcome per applied plan item"; a gap means the caller lost one.
	ErrMissingOutcome = errors.New("plangate: allowed plan item has no apply outcome")

	// ErrUnmatchedOutcome is returned by Result when an ApplyOutcome's
	// call id matches no item in the decided plan.
	ErrUnmatchedOutcome = errors.New("plangate: apply outcome matches no plan item")

	// ErrInvalidOutcome is returned by Result when an ApplyOutcome
	// carries neither a ToolResult nor a ToolError, or carries both.
	ErrInvalidOutcome = errors.New("plangate: apply outcome must carry exactly one of result or error")
)
