package plangate

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// PrecheckCall is one data_source or interactive call awaiting its policy
// precheck.
type PrecheckCall struct {
	// Call is the call itself. MUST NOT be nil.
	Call *toolv1.ToolCall
	// Provider is the operation's agent.hcl local name.
	Provider string
	// Schema is the originating operation's declared schema, read for
	// its kind and risk — the two policy match criteria a ToolCall does
	// not itself carry.
	Schema *toolv1.ToolSchema
}

// PrecheckResult is one call's precheck outcome, in the narrowed
// allow/deny space [plan-apply-gate.md#data-source-and-interactive-calls]
// requires: neither kind has an apply step to gate, so ask is not a
// meaningful decision for either.
type PrecheckResult struct {
	// Call is the call this result is for, echoed from the request.
	Call *toolv1.ToolCall
	// Allowed reports whether the call may proceed. False only for a
	// deny — including an ask the policy engine downgraded to one.
	Allowed bool
	// Rule is the name of the policy rule responsible for the outcome,
	// empty when no rule matched and the kind default applied.
	Rule string
	// Downgraded reports that a winning ask decision was flipped to deny
	// because this call has no apply step to gate.
	// [plan-apply-gate.md#data-source-and-interactive-calls] makes
	// distinguishing this from a plain deny a MUST, so a caller can log
	// that the transition happened rather than have it happen silently.
	Downgraded bool
	// Tripped reports that this denial crossed one of the provider's
	// circuit-breaker thresholds
	// ([plan-apply-gate.md#circuit-breaker-on-repeated-denials]). Always
	// false for an allow, and always false when the Gate has no Breaker.
	// A caller routes a trip through its limit-reached path; this package
	// only reports it.
	Tripped bool
	// Denial is the synthesized ToolError carrying the denial reason,
	// non-nil exactly when Allowed is false. A caller turns it into the
	// tool_result denial block the model observes — see DenialBlocks for
	// the resource-item equivalent.
	Denial *toolv1.ToolError
}

// Precheck evaluates each call in calls against policy and returns one
// result per call, in the same order.
//
// It returns no error: every outcome is a decision, and a malformed entry
// (a nil Call) is reported as a denial on that entry rather than failing
// the whole batch, so one bad call never blocks the turn's other reads.
//
// The ask-to-deny downgrade is NOT implemented here. policy.Evaluate
// already performs it for TOOL_KIND_DATA_SOURCE and TOOL_KIND_INTERACTIVE
// calls and reports it through its third return value; re-deriving it here
// would be a second, divergable definition of the same rule. This function
// only surfaces what the policy engine reported.
//
// SESSION-scope verdicts are deliberately NOT consulted here. A SESSION
// verdict is recorded only when the resolver resolves an ask, and an ask
// only ever reaches the resolver for a resource item — an operation's kind
// is a property of the operation, so a (provider, operation_name) pair
// that produced a SESSION verdict can never also be the data_source or
// interactive operation a precheck is evaluating. Consulting the map here
// would be unreachable code, not extra safety.
func (g *Gate) Precheck(ctx context.Context, calls []PrecheckCall) []PrecheckResult {
	ctx, span := g.telem.StartPolicyEvaluate(ctx)
	defer telemetry.EndSpan(span, nil)

	results := make([]PrecheckResult, 0, len(calls))
	for _, c := range calls {
		results = append(results, g.precheckOne(ctx, c))
	}
	return results
}

// precheckOne evaluates one call. Split out of Precheck so the loop body
// stays a single statement and the per-call branches stay readable.
func (g *Gate) precheckOne(ctx context.Context, c PrecheckCall) PrecheckResult {
	if c.Call == nil {
		g.logger.ErrorContext(ctx, "plangate: precheck: nil call denied",
			"session_id", g.sessionID, "provider", c.Provider)
		return PrecheckResult{
			Allowed: false,
			Denial:  denialError("plangate: precheck: call is missing; denied"),
		}
	}

	toolName := c.Call.GetToolName()
	action, rule, downgraded := policy.Evaluate(g.rules, policy.Call{
		Kind:     c.Schema.GetKind(),
		Provider: c.Provider,
		ToolName: toolName,
		Risk:     c.Schema.GetRisk(),
	})

	res := PrecheckResult{
		Call:       c.Call,
		Allowed:    action == policy.ActionAllow,
		Rule:       rule,
		Downgraded: downgraded,
	}
	g.countDecision(ctx, decisionMetricValue(action))

	if res.Allowed {
		g.logger.DebugContext(ctx, "plangate: precheck allowed",
			"session_id", g.sessionID, "provider", c.Provider, "operation", toolName,
			"rule", rule)
		return res
	}

	res.Tripped = g.recordDenial(c.Provider)
	res.Denial = denialError(fmt.Sprintf(
		"policy denied %s.%s (%s); this call was not executed", c.Provider, toolName, decidedBy(rule)))
	g.logger.WarnContext(ctx, "plangate: precheck denied",
		"session_id", g.sessionID, "provider", c.Provider, "operation", toolName,
		"rule", rule, "downgraded_from_ask", downgraded, "breaker_tripped", res.Tripped)
	return res
}

// countDecision records one policy decision on the shared
// policy_decisions counter. The decision value is a fixed three-value
// vocabulary; nothing unbounded (provider, operation, session) becomes a
// metric attribute here (.claude/rules/logging-telemetry.md's cardinality
// discipline).
func (g *Gate) countDecision(ctx context.Context, decision string) {
	g.telem.Instruments().PolicyDecisions.Add(ctx, 1,
		metric.WithAttributes(telemetry.PolicyDecisionKey.String(decision)))
}

// decisionMetricValue maps a policy action onto PolicyDecisionKey's
// three-value vocabulary. ActionUnspecified cannot occur — Evaluate always
// returns one of allow/ask/deny — and maps to deny so an impossible value
// can never be counted as the permissive one.
func decisionMetricValue(action policy.Action) string {
	switch action {
	case policy.ActionAllow:
		return telemetry.PolicyDecisionAllow
	case policy.ActionAsk:
		return telemetry.PolicyDecisionAsk
	case policy.ActionDeny, policy.ActionUnspecified:
		return telemetry.PolicyDecisionDeny
	}
	return telemetry.PolicyDecisionDeny
}
