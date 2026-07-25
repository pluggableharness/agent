package turn

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/callhash"
	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/plangate"
	"github.com/pluggableharness/agent/internal/tooldispatch"
)

// GateHooks adapts a HookDispatcher to the HookDispatcher
// internal/plangate declares for itself, so a session wiring both packages
// can pass turn.GateHooks{Dispatcher: hooks} straight into
// plangate.Config.Hooks and have one dispatcher serve both.
//
// It exists because plangate deliberately does NOT import
// internal/hookdispatch — the gate needs a plan-ready verdict, not a
// dispatcher's whole surface, and keeping it that way is what lets the gate
// be tested against a twenty-line fake. This package owns both sides of
// that boundary, so the bridge belongs here. No import cycle results:
// plangate imports neither hookdispatch nor this package, and this package
// imports both.
type GateHooks struct {
	// Dispatcher is the real chain runner.
	Dispatcher HookDispatcher
}

// Dispatch runs the chain and converts the outcome. An error is propagated
// unchanged and with a zero HookOutcome, never turned into an implicit
// verdict — plangate's own contract is that Decision is meaningless when
// err is non-nil.
func (g GateHooks) Dispatch(ctx context.Context, payload *hookv1.HookPayload) (plangate.HookOutcome, error) {
	out, err := g.Dispatcher.Dispatch(ctx, payload)
	if err != nil {
		return plangate.HookOutcome{}, err
	}
	return hookOutcome(out), nil
}

// hookOutcome converts a hookdispatch.Outcome to the field-for-field
// identical plangate.HookOutcome. Both carry {Payload, Decision, DeniedBy}
// in that order and with those types, which is why this is a single Go
// struct conversion rather than a hand-written field copy — plangate's own
// doc comment on HookOutcome commits to keeping it that way. If this stops
// compiling, the two types have diverged and the fix belongs in whichever
// one drifted, not in a field-by-field adapter here.
func hookOutcome(o hookdispatch.Outcome) plangate.HookOutcome {
	return plangate.HookOutcome(o)
}

// applyOutcome converts one tooldispatch.Outcome to the
// plangate.ApplyOutcome the gate's step-14 ApplyResult is built from. This
// one IS a field copy, deliberately: tooldispatch.Outcome carries two
// fields plangate has no place for — ExitCode (an exec-family detail) and
// Sequence (the persisted tool_result event's state-backend sequence, which
// the gate never reads because it persists its own apply event).
// plangate.ApplyOutcome's exactly-one-of-Result-or-Error invariant is
// already guaranteed by tooldispatch.Outcome's own contract, so nothing is
// re-validated here that the scheduler did not already establish.
func applyOutcome(o tooldispatch.Outcome) plangate.ApplyOutcome {
	return plangate.ApplyOutcome{
		Call:   o.Call,
		Result: o.Result,
		Error:  o.Error,
	}
}

// errorBlock synthesizes the model-visible tool_result block for a call
// that produced a ToolError rather than a result. It matches
// plangate.DenialBlocks' shape exactly — the call's id, one text block
// carrying the reason, is_error set — because
// plan-apply-gate.md#decision-semantics makes tool-result text the ONLY
// channel a denial or failure travels on: the model observes it in its own
// history and adapts on the next turn rather than watching a call silently
// vanish.
func errorBlock(callID string, err *toolv1.ToolError) *contentv1.ContentBlock {
	return toolResultBlock(callID, err.GetMessage(), true)
}

// resultBlock synthesizes the model-visible tool_result block for a
// successful call. tool.v1.ToolResult carries exactly one field — a
// structpb payload conforming to the operation's output_schema — so the
// model sees that payload's canonical JSON encoding.
func resultBlock(callID string, res *toolv1.ToolResult) *contentv1.ContentBlock {
	return toolResultBlock(callID, resultText(res), false)
}

// toolResultBlock builds one tool_result content block.
func toolResultBlock(callID, text string, isError bool) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_ToolResult{
			ToolResult: &contentv1.ToolResultBlock{
				ToolUseId: callID,
				Content: []*contentv1.ContentBlock{{
					Block: &contentv1.ContentBlock_Text{
						Text: &contentv1.TextBlock{Text: text},
					},
				}},
				IsError: isError,
			},
		},
	}
}

// resultText renders a ToolResult's payload as the text the model reads.
// It goes through internal/callhash's Canonical encoder rather than
// encoding/json directly: that package is the codebase's single
// deterministic structpb encoding (sorted object keys, no dependence on Go
// map iteration order), and determinism.md forbids a second one — a
// tool_result block lands in the persisted conversation history, so its
// bytes must not vary between two runs of the same session.
func resultText(res *toolv1.ToolResult) string {
	return string(callhash.Canonical(&structpb.Value{
		Kind: &structpb.Value_StructValue{StructValue: res.GetPayload()},
	}))
}
