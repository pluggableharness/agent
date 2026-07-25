package plangate

import (
	"context"
	"fmt"

	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// ApplyOutcome is one applied call's terminal outcome, as this package
// consumes it.
//
// It is this package's OWN shape, deliberately not internal/tooldispatch's
// — see doc.go. A caller that runs the plan converts its scheduler's
// outcomes into this before calling Result; that conversion belongs to
// whoever owns both sides, not to the gate.
//
// Exactly one of Result and Error MUST be set: a call either succeeded or
// failed on its own terms, and a plan item that was never executed is a
// denial, which the gate already knows about and never expects here.
type ApplyOutcome struct {
	// Call is the call that was applied. Its id matches the originating
	// PlanItem.call_id, which is how Result pairs the two.
	Call *toolv1.ToolCall
	// Result is the call's successful terminal result.
	Result *toolv1.ToolResult
	// Error is the call's failed terminal result.
	Error *toolv1.ToolError
}

// Result assembles the turn's ApplyResult from out plus d's denied items,
// persists it as one apply event, and returns it for the caller to feed
// into the post-apply hook payload
// (pluggableharness.hook.v1.PostApplyPayload reuses this exact message).
//
// Items appear in plan order, not in completion order: apply outcomes may
// arrive out of wall-clock order for concurrently-executed calls, and the
// persisted record must not depend on which finished first
// (.claude/rules/determinism.md).
//
// Every allowed item MUST have a matching outcome and every outcome MUST
// match an item — plan.v1.ApplyResult carries "one outcome per applied
// plan item", so a gap or a stray is a lost result in the caller, not
// something to quietly drop from the audit record.
// APPLY_OUTCOME_SKIPPED is never produced: it is reserved for a future
// partial-apply-then-abort mode this build does not implement.
func (g *Gate) Result(ctx context.Context, turnID string, d Decisions, out []ApplyOutcome) (_ *planv1.ApplyResult, err error) {
	ctx, span := g.telem.StartPlanApply(ctx, turnID)
	defer func() { telemetry.EndSpan(span, err) }()
	g.logger.DebugContext(ctx, "plangate: assembling apply result",
		"session_id", g.sessionID, "turn_id", turnID,
		"outcome_count", len(out), "denied_count", len(d.Denied))

	byCall, err := indexOutcomes(out)
	if err != nil {
		return nil, err
	}

	items, err := g.applyItems(d, byCall)
	if err != nil {
		return nil, err
	}
	if len(byCall) > 0 {
		return nil, fmt.Errorf("plangate: result: %d outcome(s) left over, first call id %q: %w",
			len(byCall), anyKey(byCall), ErrUnmatchedOutcome)
	}

	result := &planv1.ApplyResult{TurnId: turnID, Items: items}
	if err := g.persistApply(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

// indexOutcomes keys out by call id, rejecting a malformed entry up front
// so the pairing loop below never has to re-check the oneof invariant.
func indexOutcomes(out []ApplyOutcome) (map[string]ApplyOutcome, error) {
	byCall := make(map[string]ApplyOutcome, len(out))
	for i, o := range out {
		if (o.Result == nil) == (o.Error == nil) {
			return nil, fmt.Errorf("plangate: result: outcome %d: %w", i, ErrInvalidOutcome)
		}
		byCall[o.Call.GetId()] = o
	}
	return byCall, nil
}

// applyItems walks the decided plan in order and pairs each item with its
// outcome, consuming entries from byCall so whatever remains afterwards is
// exactly the set of unmatched outcomes.
func (g *Gate) applyItems(d Decisions, byCall map[string]ApplyOutcome) ([]*planv1.ApplyResult_ApplyItem, error) {
	denied := make(map[string]struct{}, len(d.Denied))
	for _, di := range d.Denied {
		denied[di.Item.GetId()] = struct{}{}
	}

	items := make([]*planv1.ApplyResult_ApplyItem, 0, len(d.Plan.GetItems()))
	for _, item := range d.Plan.GetItems() {
		if _, isDenied := denied[item.GetId()]; isDenied {
			items = append(items, &planv1.ApplyResult_ApplyItem{
				PlanItemId: item.GetId(),
				CallId:     item.GetCallId(),
				Outcome:    planv1.ApplyResult_APPLY_OUTCOME_DENIED,
			})
			continue
		}

		o, ok := byCall[item.GetCallId()]
		if !ok {
			return nil, fmt.Errorf("plangate: result: item %q (%s.%s): %w",
				item.GetId(), item.GetProvider(), item.GetOperationName(), ErrMissingOutcome)
		}
		delete(byCall, item.GetCallId())
		items = append(items, applyItem(item, o))
	}
	return items, nil
}

// applyItem builds one ApplyItem from a plan item and its outcome. The
// oneof invariant (exactly one of Result/Error) was already enforced by
// indexOutcomes, so a nil Result here means the outcome carried an Error.
func applyItem(item *planv1.PlanItem, o ApplyOutcome) *planv1.ApplyResult_ApplyItem {
	ai := &planv1.ApplyResult_ApplyItem{
		PlanItemId: item.GetId(),
		CallId:     item.GetCallId(),
	}
	if o.Result != nil {
		ai.Outcome = planv1.ApplyResult_APPLY_OUTCOME_APPLIED
		ai.Result = &planv1.ApplyResult_ApplyItem_ToolResult{ToolResult: o.Result}
		return ai
	}
	ai.Outcome = planv1.ApplyResult_APPLY_OUTCOME_FAILED
	ai.Result = &planv1.ApplyResult_ApplyItem_ToolError{ToolError: o.Error}
	return ai
}

// persistApply writes the turn's apply event.
func (g *Gate) persistApply(ctx context.Context, result *planv1.ApplyResult) error {
	payload, err := statebackend.MarshalPayload(&eventv1.ApplyEvent{Result: result})
	if err != nil {
		return fmt.Errorf("plangate: result: marshal apply event: %w", err)
	}

	now := g.clock()
	ev := statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kernelv1.EventKind_EVENT_KIND_APPLY,
		Producer:      statebackend.KernelProducer(),
		SchemaVersion: planEventSchemaVersion,
		Payload:       payload,
	}
	if _, err := g.events.AppendEvent(ctx, ev); err != nil {
		return fmt.Errorf("plangate: result: append apply event: %w", err)
	}
	return nil
}

// anyKey returns one key from m, for naming the first offender in an
// error message. Which key it is does not matter — the error reports a
// count alongside it — so map iteration order is not a determinism
// concern here: this value is never persisted.
func anyKey(m map[string]ApplyOutcome) string {
	for k := range m {
		return k
	}
	return ""
}
