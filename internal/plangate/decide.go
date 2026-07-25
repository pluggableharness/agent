package plangate

import (
	"context"
	"fmt"
	"sort"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// Decisions is one plan's fully-resolved decision set. Every item on Plan
// carries a terminal decision (ALLOW or DENY) by the time Decide returns —
// never PENDING and never ASK, because a plan_items row only ever holds a
// made decision.
type Decisions struct {
	// Plan is the decided plan, the same *planv1.Plan Decide was given
	// with each item's decision and decided_by stamped on in place.
	Plan *planv1.Plan
	// Allowed are the items the caller may apply, in plan order.
	Allowed []*planv1.PlanItem
	// Denied are the items that MUST NOT be applied, in plan order.
	Denied []DeniedItem
	// VetoedBy is non-empty when the plan-ready chain denied the WHOLE
	// plan: the subscriber name behind every item's
	// "hook-veto:<provider>" decided_by. A veto overrides every per-item
	// policy decision, including ALLOW ones.
	VetoedBy string
}

// DeniedItem is one denied plan item and the denial the model will see.
type DeniedItem struct {
	// Item is the denied plan item.
	Item *planv1.PlanItem
	// Reason is the human-readable denial text, also carried on Error
	// and rendered into the synthesized tool_result block.
	Reason string
	// Error is the synthesized ToolError for this denial.
	Error *toolv1.ToolError
	// Tripped reports that this denial crossed one of the provider's
	// circuit-breaker thresholds
	// ([plan-apply-gate.md#circuit-breaker-on-repeated-denials]). See
	// Decisions.TrippedProviders for the plan-wide view.
	Tripped bool
}

// TrippedProviders returns the sorted, deduplicated set of provider names
// whose circuit breaker tripped while deciding this plan. Empty when
// nothing tripped.
//
// [plan-apply-gate.md#circuit-breaker-on-repeated-denials] wants a trip to
// route through the same graceful-degradation path a bound uses. That path
// is the session driver's, not this package's — so a trip is reported
// here, never acted on here, and never swallowed. Sorted because a caller
// may log or persist it and Go map order must not leak into either
// (.claude/rules/determinism.md).
func (d Decisions) TrippedProviders() []string {
	var names []string
	seen := make(map[string]struct{}, len(d.Denied))
	for _, di := range d.Denied {
		if !di.Tripped {
			continue
		}
		name := di.Item.GetProvider()
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Decide evaluates plan and returns its terminal decision set, running, in
// this order:
//
//  1. policy, per PlanItem — never once for the whole plan. A plan with
//     three resource calls against three providers receives three
//     independently evaluated decisions
//     ([plan-apply-gate.md#plan-construction-and-policy-evaluation]).
//  2. the plan-ready hook chain, exactly once. Any HOOK_DECISION_DENY
//     denies the whole plan and sets VetoedBy, whatever the per-item
//     decisions were. Chain ordering — policy pinned ahead of every
//     plugin subscriber — is the dispatcher's guarantee, not re-derived
//     here.
//  3. resolution of every remaining ASK item: a remembered SESSION-scope
//     verdict first, otherwise the plan-decision resolver, with any
//     corrected_input re-validated via plandecision.ValidateDecision.
//  4. persistence: one plan event plus one plan_items row per item, in a
//     single AppendPlan transaction, every decision terminal.
//
// Every ask is resolved BEFORE anything is persisted, deliberately: the
// plan_items table has no representation for a decision that is still
// pending, so an ask resolved mid-turn would have nowhere to go if the row
// had already been written.
//
// An ALWAYS-scoped resolver verdict returns
// plandecision.ErrPolicyPersistenceUnavailable and persists nothing. This
// build has no writable policy store, and
// [plan-apply-gate.md#plandecisionscope-semantics] forbids silently
// downgrading such a verdict to SESSION or ONCE — an operator needs to
// know an "always allow this" request did not stick.
func (g *Gate) Decide(ctx context.Context, plan *planv1.Plan) (_ Decisions, err error) {
	ctx, span := g.telem.StartPolicyEvaluate(ctx)
	defer func() { telemetry.EndSpan(span, err) }()

	if plan == nil {
		return Decisions{}, fmt.Errorf("plangate: decide: %w", ErrNilItem)
	}
	g.logger.DebugContext(ctx, "plangate: deciding plan",
		"session_id", g.sessionID, "turn_id", plan.GetTurnId(), "item_count", len(plan.GetItems()))

	if err := g.evaluateItems(ctx, plan); err != nil {
		return Decisions{}, err
	}

	vetoedBy, err := g.planReady(ctx, plan)
	if err != nil {
		return Decisions{}, err
	}

	if vetoedBy == "" {
		if err := g.resolveAsks(ctx, plan); err != nil {
			return Decisions{}, err
		}
	}

	d, err := g.collect(ctx, plan, vetoedBy)
	if err != nil {
		return Decisions{}, err
	}
	if err := g.persistPlan(ctx, plan); err != nil {
		return Decisions{}, err
	}

	// The circuit breaker is debited only once the plan is durably
	// recorded, deliberately. collect runs first because it is what
	// rejects a non-terminal decision before the AppendPlan write — but
	// debiting inside it would leave the breaker counting denials from a
	// plan that failed to persist, so a run of failed appends could trip a
	// provider on denials with no audit row to explain the trip.
	g.recordDenials(d)
	return d, nil
}

// recordDenials debits each denial against its provider's circuit breaker
// and stamps the resulting trip onto its DeniedItem, in plan order. Called
// only after the plan is persisted — see Decide.
func (g *Gate) recordDenials(d Decisions) {
	for i := range d.Denied {
		d.Denied[i].Tripped = g.recordDenial(d.Denied[i].Item.GetProvider())
	}
}

// evaluateItems runs policy once per item, stamping each item's decision
// and its "policy:<rule>"/"policy:default" decided_by in place.
func (g *Gate) evaluateItems(ctx context.Context, plan *planv1.Plan) error {
	for i, item := range plan.GetItems() {
		if item == nil {
			return fmt.Errorf("plangate: decide: item %d: %w", i, ErrNilItem)
		}
		action, rule, downgraded := policy.Evaluate(g.rules, policy.Call{
			Kind:     item.GetKind(),
			Provider: item.GetProvider(),
			ToolName: item.GetOperationName(),
			Risk:     item.GetRisk(),
		})
		g.countDecision(ctx, decisionMetricValue(action))

		item.DecidedBy = decidedBy(rule)
		switch action {
		case policy.ActionAllow:
			item.Decision = planv1.PlanDecision_PLAN_DECISION_ALLOW
		case policy.ActionAsk:
			item.Decision = planv1.PlanDecision_PLAN_DECISION_ASK
		case policy.ActionDeny, policy.ActionUnspecified:
			// ActionUnspecified cannot occur — Evaluate always returns
			// one of allow/ask/deny — and lands here so an impossible
			// value can never be treated as the permissive one.
			item.Decision = planv1.PlanDecision_PLAN_DECISION_DENY
		}

		if downgraded {
			// Only reachable for a data_source/interactive item that a
			// caller routed through the plan path rather than Precheck.
			// The verdict stands; the downgrade is logged because
			// configuration.md §7.3 asks the kernel to say why.
			g.logger.WarnContext(ctx, "plangate: ask downgraded to deny; call has no apply step to gate",
				"session_id", g.sessionID, "provider", item.GetProvider(),
				"operation", item.GetOperationName(), "rule", rule)
		}
	}
	return nil
}

// planReady dispatches the plan-ready chain once and reports the vetoing
// subscriber's name, empty when the chain allowed the plan. A veto
// rewrites every item to DENY with a "hook-veto:<provider>" decided_by —
// including items policy had already allowed, since a plan-ready veto is
// coarse and covers the whole plan.
func (g *Gate) planReady(ctx context.Context, plan *planv1.Plan) (string, error) {
	out, err := g.hooks.Dispatch(ctx, &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_PlanReady{
			PlanReady: &hookv1.PlanReadyPayload{Plan: plan},
		},
	})
	if err != nil {
		// A dispatcher-level failure is not a verdict: out.Decision is
		// meaningless here, and treating the error as an implicit
		// allow or deny would invent a decision nobody made.
		return "", fmt.Errorf("plangate: decide: plan-ready dispatch: %w", err)
	}
	if out.Decision != hookv1.HookDecision_HOOK_DECISION_DENY {
		return "", nil
	}

	vetoedBy := out.DeniedBy
	for _, item := range plan.GetItems() {
		item.Decision = planv1.PlanDecision_PLAN_DECISION_DENY
		item.DecidedBy = hookVetoDecidedBy(vetoedBy)
	}
	g.logger.WarnContext(ctx, "plangate: plan-ready veto denied the whole plan",
		"session_id", g.sessionID, "turn_id", plan.GetTurnId(),
		"vetoed_by", vetoedBy, "item_count", len(plan.GetItems()))
	return vetoedBy, nil
}

// resolveAsks turns every remaining ASK item terminal.
func (g *Gate) resolveAsks(ctx context.Context, plan *planv1.Plan) error {
	for _, item := range plan.GetItems() {
		if item.GetDecision() != planv1.PlanDecision_PLAN_DECISION_ASK {
			continue
		}
		if g.applyScoped(ctx, item) {
			continue
		}
		if err := g.resolveAsk(ctx, plan.GetTurnId(), item); err != nil {
			return err
		}
	}
	return nil
}

// applyScoped applies a remembered SESSION-scope verdict to item, if one
// exists for its (provider, operation_name) pair, and reports whether it
// did. A hit suppresses the resolver round trip entirely — that suppression
// is the whole point of the SESSION scope
// ([plan-apply-gate.md#plandecisionscope-semantics]: "without re-emitting a
// permission_request/blocking on a fresh plan_decision").
func (g *Gate) applyScoped(ctx context.Context, item *planv1.PlanItem) bool {
	v, ok := g.recallScope(item.GetProvider(), item.GetOperationName())
	if !ok {
		return false
	}
	item.Decision = v.decision
	item.DecidedBy = item.GetDecidedBy() + "+session:" + v.decidedBy
	g.logger.DebugContext(ctx, "plangate: session-scoped verdict applied",
		"session_id", g.sessionID, "provider", item.GetProvider(),
		"operation", item.GetOperationName(), "decision", item.GetDecision())
	return true
}

// resolveAsk escalates one ask item to the plan-decision resolver.
func (g *Gate) resolveAsk(ctx context.Context, turnID string, item *planv1.PlanItem) error {
	req := plandecision.Request{
		SessionID:   g.sessionID,
		TurnID:      turnID,
		Item:        item,
		InputSchema: g.inputSchema(ctx, item),
	}

	rctx, span := g.telem.StartPlanDecisionResolve(ctx, item.GetId())
	dec, err := g.resolver.Resolve(rctx, req)
	telemetry.EndSpan(span, err)
	if err != nil {
		return fmt.Errorf("plangate: decide: resolve %s.%s: %w",
			item.GetProvider(), item.GetOperationName(), err)
	}

	// Validate before anything is applied: an invalid corrected_input is
	// rejected as a distinct error, never coerced and never silently
	// turned into a plain deny
	// ([frontend-protocol.md#plan_decisioncorrected_input]). The wrapped
	// schemavalidate.ErrValidation stays matchable with errors.Is.
	if err := plandecision.ValidateDecision(req, dec); err != nil {
		return fmt.Errorf("plangate: decide: %s.%s: %w",
			item.GetProvider(), item.GetOperationName(), err)
	}
	if dec.Scope == frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS {
		return fmt.Errorf("plangate: decide: %s.%s: %w",
			item.GetProvider(), item.GetOperationName(), plandecision.ErrPolicyPersistenceUnavailable)
	}

	item.Decision = dec.Decision
	item.DecidedBy += "+resolver:" + dec.DecidedBy
	if dec.CorrectedInput != nil {
		item.Input = dec.CorrectedInput
	}
	if dec.Scope == frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION {
		g.rememberScope(item.GetProvider(), item.GetOperationName(), sessionVerdict{
			decision:  dec.Decision,
			decidedBy: dec.DecidedBy,
		})
	}
	g.logger.DebugContext(ctx, "plangate: ask resolved",
		"session_id", g.sessionID, "provider", item.GetProvider(),
		"operation", item.GetOperationName(), "decision", item.GetDecision(),
		"scope", dec.Scope, "decided_by", item.GetDecidedBy())
	return nil
}

// inputSchema resolves item's declared input schema for corrected_input
// re-validation, or nil when this Gate has no catalog or the operation is
// no longer resolvable. A nil schema means "no constraint to check against"
// — plandecision.ValidateDecision's own documented behavior — so the
// lookup failure is logged here rather than propagated: an operation that
// vanished from the catalog mid-turn must not turn a legitimate operator
// approval into a turn-level error.
func (g *Gate) inputSchema(ctx context.Context, item *planv1.PlanItem) *schemav1.Schema {
	if g.tools == nil {
		return nil
	}
	handle, err := g.tools.Tool(item.GetProvider(), item.GetOperationName())
	if err != nil {
		g.logger.WarnContext(ctx, "plangate: no input schema for corrected_input re-validation",
			"session_id", g.sessionID, "provider", item.GetProvider(),
			"operation", item.GetOperationName(), "err", err)
		return nil
	}
	return handle.Schema.GetInputSchema()
}

// collect partitions the decided plan into its allowed and denied halves.
// It rejects any item still carrying a non-terminal decision — that is a
// bug in this package, caught before the AppendPlan write so a PENDING or
// ASK row can never reach plan_items.
//
// It deliberately does NOT touch the circuit breaker: Decide debits it
// after the plan persists, so a failed append cannot leave the breaker
// counting denials for a plan that was never recorded.
func (g *Gate) collect(ctx context.Context, plan *planv1.Plan, vetoedBy string) (Decisions, error) {
	d := Decisions{Plan: plan, VetoedBy: vetoedBy}
	for _, item := range plan.GetItems() {
		switch item.GetDecision() {
		case planv1.PlanDecision_PLAN_DECISION_ALLOW:
			d.Allowed = append(d.Allowed, item)
		case planv1.PlanDecision_PLAN_DECISION_DENY:
			reason := fmt.Sprintf("%s.%s was denied (%s); this call was not executed",
				item.GetProvider(), item.GetOperationName(), item.GetDecidedBy())
			d.Denied = append(d.Denied, DeniedItem{
				Item:   item,
				Reason: reason,
				Error:  denialError(reason),
			})
		default:
			return Decisions{}, fmt.Errorf("plangate: decide: item %q (%s.%s) is %v: %w",
				item.GetId(), item.GetProvider(), item.GetOperationName(), item.GetDecision(), ErrNonTerminalDecision)
		}
	}
	g.logger.DebugContext(ctx, "plangate: plan decided",
		"session_id", g.sessionID, "turn_id", plan.GetTurnId(),
		"allowed", len(d.Allowed), "denied", len(d.Denied), "vetoed_by", vetoedBy)
	return d, nil
}

// persistPlan writes the turn's plan event and every plan_items row in one
// AppendPlan transaction.
func (g *Gate) persistPlan(ctx context.Context, plan *planv1.Plan) error {
	payload, err := statebackend.MarshalPayload(&eventv1.PlanEvent{Plan: plan})
	if err != nil {
		return fmt.Errorf("plangate: decide: marshal plan event: %w", err)
	}

	rows := make([]statebackend.PlanItem, 0, len(plan.GetItems()))
	for _, item := range plan.GetItems() {
		rows = append(rows, statebackend.PlanItem{
			TurnID:       plan.GetTurnId(),
			ToolCallID:   item.GetCallId(),
			ProviderName: item.GetProvider(),
			ToolName:     item.GetOperationName(),
			Decision:     item.GetDecision(),
			DecidedBy:    item.GetDecidedBy(),
		})
	}

	now := g.clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_PLAN,
		Producer:      statebackend.KernelProducer(),
		SchemaVersion: planEventSchemaVersion,
		Payload:       payload,
	}
	if _, err := g.events.AppendPlan(ctx, ev, rows); err != nil {
		return fmt.Errorf("plangate: decide: append plan: %w", err)
	}
	return nil
}

// DenialBlocks synthesizes one tool_result content block per denied item.
//
// [plan-apply-gate.md#decision-semantics] makes this the ONLY channel a
// denial travels on: "denial surfaces as tool-result text, not a separate
// out-of-band channel", so the model observes the denial in its own
// history and can adapt on the next turn rather than watching a call
// silently vanish. Every block carries is_error, the convention several
// vendor APIs already use for exactly this.
func (g *Gate) DenialBlocks(d Decisions) []*contentv1.ContentBlock {
	blocks := make([]*contentv1.ContentBlock, 0, len(d.Denied))
	for _, di := range d.Denied {
		blocks = append(blocks, &contentv1.ContentBlock{
			Block: &contentv1.ContentBlock_ToolResult{
				ToolResult: &contentv1.ToolResultBlock{
					ToolUseId: di.Item.GetCallId(),
					Content: []*contentv1.ContentBlock{{
						Block: &contentv1.ContentBlock_Text{
							Text: &contentv1.TextBlock{Text: di.Reason},
						},
					}},
					IsError: true,
				},
			},
		})
	}
	return blocks
}

// decidedBy renders a policy rule name as its plan_items decided_by form:
// "policy:<rule-name>", or "policy:default" when no rule matched and the
// kind's default applied.
func decidedBy(rule string) string {
	if rule == "" {
		return "policy:default"
	}
	return "policy:" + rule
}

// hookVetoDecidedBy renders a plan-ready veto as its decided_by form.
func hookVetoDecidedBy(provider string) string {
	return "hook-veto:" + provider
}
