package autoallow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

// DecidedBy is written verbatim into every plan_items.decided_by row this
// resolver produces — deliberately shouty, so a session audited later
// shows unambiguously, per item, that no human ever approved it. DO NOT
// soften this string's wording.
const DecidedBy = "UNSAFE-AUTO-ALLOW(no-frontend-attached)"

// ErrNotAcknowledged is returned by New unless
// Config.AcknowledgeUnsafeAutoAllow is explicitly true. There is
// deliberately NO usable zero value for this resolver — a caller cannot
// construct a working instance by accident.
var ErrNotAcknowledged = errors.New(
	"autoallow: refusing to construct: this resolver auto-approves every ask decision " +
		"and is a deliberate deviation from plan-apply-gate.md#decision-semantics; " +
		"set Config.AcknowledgeUnsafeAutoAllow to use it")

// Config configures the auto-allow resolver.
type Config struct {
	// AcknowledgeUnsafeAutoAllow MUST be true, or New returns
	// ErrNotAcknowledged. This field existing at all is the whole point
	// — a caller must affirmatively opt in, in code, at the call site.
	AcknowledgeUnsafeAutoAllow bool
	// Logger receives the construction-time WARN and the one WARN this
	// resolver emits per resolution. Nil falls back to slog.Default() —
	// this resolver is never silent.
	Logger *slog.Logger
	// Telemetry is the Provider spans and the policy-decision counter go
	// through. Nil falls back to a Provider with every signal disabled,
	// matching internal/eventbus and internal/statebackend: the
	// instrumentation code path still runs, it just exports nothing.
	Telemetry *telemetry.Provider
}

// resolver is the Config-backed plandecision.Resolver New returns. It is
// unexported precisely so the only route to one is through New, and
// therefore through the acknowledgement check.
type resolver struct {
	logger    *slog.Logger
	telemetry *telemetry.Provider
}

// New constructs the auto-allow resolver, or returns ErrNotAcknowledged if
// cfg.AcknowledgeUnsafeAutoAllow is not explicitly true. It logs one WARN
// at construction naming the deviation, its reason, and where the real
// replacement plugs in.
func New(cfg Config) (plandecision.Resolver, error) {
	if !cfg.AcknowledgeUnsafeAutoAllow {
		return nil, ErrNotAcknowledged
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	prov := cfg.Telemetry
	if prov == nil {
		var err error
		prov, err = defaultTelemetryProvider()
		if err != nil {
			return nil, fmt.Errorf("autoallow: new: %w", err)
		}
	}

	logger.Warn("autoallow: constructing the UNSAFE auto-allow plan-decision resolver",
		slog.String("deviation", "plan-apply-gate.md#decision-semantics requires an ask decision to emit a permission-request state event and block on a frontend client decision"),
		slog.String("reason", "no frontend attach path exists in this build; this stage cannot satisfy that MUST"),
		slog.String("replacement", "internal/plandecision/drivers/frontend"),
		slog.String("decided_by", DecidedBy),
	)

	return &resolver{logger: logger, telemetry: prov}, nil
}

// defaultTelemetryProvider builds the Provider used when Config.Telemetry
// is nil, following internal/statebackend's and internal/eventbus's
// function of the same name exactly: every signal disabled, so
// telemetry.New never actually calls into the noop.Backend passed here.
func defaultTelemetryProvider() (*telemetry.Provider, error) {
	return telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
}

// Resolve auto-approves req unconditionally: PLAN_DECISION_ALLOW, scoped
// PLAN_DECISION_SCOPE_ONCE, with no corrected input and DecidedBy stamped
// on it, after logging one WARN naming the item it just waved through. It
// never denies and never asks anyone anything. The only errors it returns
// are a malformed request (no plan item) and an already-cancelled ctx.
func (r *resolver) Resolve(ctx context.Context, req plandecision.Request) (_ plandecision.Decision, err error) {
	if err := req.Validate(); err != nil {
		return plandecision.Decision{}, fmt.Errorf("autoallow: resolve: %w", err)
	}

	ctx, span := r.telemetry.StartPlanDecisionResolve(ctx, req.Item.GetId())
	defer func() { telemetry.EndSpan(span, err) }()

	attrs := []any{
		slog.String("session_id", req.SessionID),
		slog.String("turn_id", req.TurnID),
		slog.String("plan_item_id", req.Item.GetId()),
		slog.String("provider", req.Item.GetProvider()),
		slog.String("operation_name", req.Item.GetOperationName()),
		slog.String("risk", req.Item.GetRisk().String()),
	}

	r.logger.DebugContext(ctx, "autoallow: resolving plan item", attrs...)

	// A cancelled ctx wins over the auto-allow: this resolver does no
	// real I/O, but it must still behave like a well-formed Resolver for
	// a caller exercising cancellation generically across drivers.
	if err = ctx.Err(); err != nil {
		err = fmt.Errorf("autoallow: resolve: %w", err)
		return plandecision.Decision{}, err
	}

	r.logger.WarnContext(ctx, "autoallow: auto-approving plan item WITHOUT human approval", append(attrs,
		slog.String("decided_by", DecidedBy),
	)...)

	span.SetAttributes(telemetry.PolicyDecisionKey.String(telemetry.PolicyDecisionAllow))
	r.telemetry.Instruments().PolicyDecisions.Add(ctx, 1,
		metric.WithAttributes(telemetry.PolicyDecisionKey.String(telemetry.PolicyDecisionAllow)))

	r.logger.DebugContext(ctx, "autoallow: resolved plan item", append(attrs,
		slog.String("decision", planv1.PlanDecision_PLAN_DECISION_ALLOW.String()),
		slog.String("scope", planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE.String()),
	)...)

	return plandecision.Decision{
		Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW,
		// ONCE, never SESSION or ALWAYS: a broader scope would create
		// durable state (an in-memory session-wide suppression, or a
		// persisted policy rule) that the real frontend resolver would
		// later have to discover and reconcile. Auto-allow leaves zero
		// durable trace beyond the ordinary per-item audit row.
		Scope: planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
		// Never a correction: this resolver blanket-approves the
		// model's original input, it does not propose alternatives.
		CorrectedInput: nil,
		DecidedBy:      DecidedBy,
	}, nil
}

var _ plandecision.Resolver = (*resolver)(nil)
