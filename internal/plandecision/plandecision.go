package plandecision

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/schemavalidate"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// ErrNilItem is returned when a Request carries no PlanItem. Resolving a
// verdict for nothing is a programming error in the caller, not an
// outcome a Resolver can meaningfully produce.
var ErrNilItem = errors.New("plandecision: request has no plan item")

// ErrNonTerminalDecision is returned when a Decision carries anything
// other than PLAN_DECISION_ALLOW or PLAN_DECISION_DENY. A Resolver's job
// is to *end* the ask, so PENDING/ASK/UNSPECIFIED coming back out of one
// is a bug in that Resolver — never a state for a caller to route around
// by re-asking.
var ErrNonTerminalDecision = errors.New("plandecision: decision is not terminal")

// ErrPolicyPersistenceUnavailable is returned when a Resolver would
// produce a PLAN_DECISION_SCOPE_ALWAYS decision but has no writable
// policy store to persist it durably.
// [docs/specifications/agent-loop/plan-apply-gate.md#plandecisionscope-semantics]
// requires this be a distinct, surfaced error: a kernel build that cannot
// durably write a new policy rule "MUST reject an ALWAYS-scoped
// plan_decision with a distinct error rather than silently downgrading it
// to SESSION or ONCE" — a frontend and its operator need to know an
// "always allow this" request didn't stick, not discover it the next time
// the same prompt reappears. NEVER convert this into a silent downgrade.
var ErrPolicyPersistenceUnavailable = errors.New("plandecision: policy persistence unavailable for an ALWAYS-scoped decision")

// Request is everything a Resolver needs to resolve one
// PLAN_DECISION_ASK plan item to a terminal verdict.
type Request struct {
	// SessionID is the session the plan item belongs to, used for
	// correlation on spans and log records (never as a metric
	// attribute — it is unbounded).
	SessionID string
	// TurnID is the turn whose plan produced Item, likewise unbounded
	// and likewise span/log only.
	TurnID string
	// Item is the plan item awaiting a verdict. MUST NOT be nil.
	Item *planv1.PlanItem
	// InputSchema is the originating operation's declared input schema,
	// used to re-validate a resolver-returned CorrectedInput
	// (frontend/frontend-protocol.md#plan_decisioncorrected_input)
	// before it's honored. May be nil if the operation declared none.
	InputSchema *schemav1.Schema
}

// Validate reports whether r is well-formed enough for a Resolver to act
// on, returning ErrNilItem if it carries no PlanItem.
func (r Request) Validate() error {
	if r.Item == nil {
		return ErrNilItem
	}
	return nil
}

// Decision is a Resolver's terminal verdict for one plan item.
type Decision struct {
	// Decision MUST be PLAN_DECISION_ALLOW or PLAN_DECISION_DENY —
	// never PENDING/ASK/UNSPECIFIED. A caller receiving anything else
	// from a Resolver implementation is a programming error in that
	// implementation, not a valid outcome to route around; see
	// ValidateDecision and ErrNonTerminalDecision.
	Decision planv1.PlanDecision
	// Scope governs how durably this verdict applies beyond the one
	// PlanItem it names (plan-apply-gate.md#plandecisionscope-semantics):
	// ONCE for the named item only, SESSION for the rest of this session
	// in memory, ALWAYS persisted as policy. A Resolver that cannot
	// durably persist an ALWAYS verdict MUST fail with
	// ErrPolicyPersistenceUnavailable rather than downgrade the scope.
	Scope planv1.PlanDecisionScope
	// CorrectedInput, when non-nil, replaces the plan item's original
	// input: the operator supplied corrected arguments rather than a
	// binary accept/reject. It MUST be re-validated against the
	// originating operation's input schema before it is honored — see
	// ValidateDecision.
	CorrectedInput *structpb.Struct
	// DecidedBy identifies which resolver/mechanism produced this
	// verdict, for the plan_items audit row a future caller persists
	// (state-backend.md's plan_items.decided_by column).
	DecidedBy string
}

// ValidateDecision checks that dec is a verdict a caller may act on for
// req: terminal per ErrNonTerminalDecision, and — when dec proposes a
// CorrectedInput and req declares an InputSchema — carrying a correction
// that actually satisfies that schema.
// [docs/specifications/frontend/frontend-protocol.md#plan_decisioncorrected_input]
// makes this re-validation a MUST, with an invalid correction rejected as
// a distinct error, "never silently coerced and never silently downgraded
// to a plain deny" — hence a returned error here rather than a mutated
// Decision. The schema error is wrapped verbatim, so a caller can still
// match it with errors.Is against
// [github.com/pluggableharness/agent/internal/schemavalidate.ErrValidation].
func ValidateDecision(req Request, dec Decision) error {
	switch dec.Decision {
	case planv1.PlanDecision_PLAN_DECISION_ALLOW, planv1.PlanDecision_PLAN_DECISION_DENY:
	default:
		return fmt.Errorf("plandecision: validate decision: %q: %w", dec.Decision, ErrNonTerminalDecision)
	}

	if dec.CorrectedInput == nil || req.InputSchema == nil {
		return nil
	}

	if err := schemavalidate.Validate(structpb.NewStructValue(dec.CorrectedInput), req.InputSchema); err != nil {
		return fmt.Errorf("plandecision: validate decision: corrected_input: %w", err)
	}
	return nil
}

// Resolver resolves one PLAN_DECISION_ASK item to a terminal verdict.
//
// The spec-correct implementation (a future drivers/frontend, NOT built
// yet) emits a permission-request ServerEvent and blocks on the matching
// ClientEvent.plan_decision. Every implementation MUST honor ctx
// cancellation promptly — a hanging Resolver stalls the whole turn — and
// MUST return a Decision satisfying ValidateDecision, or an error.
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Decision, error)
}
