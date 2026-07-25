package turn

import (
	"context"
	"errors"
	"testing"

	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/hookdispatch"
	"github.com/pluggableharness/agent/internal/plangate"
	"github.com/pluggableharness/agent/internal/tooldispatch"
)

// Compile-time anchors for the two bridges this package exists to hold
// together. If either interface drifts, this file fails to build rather
// than a wiring site failing at run time.
var (
	_ plangate.HookDispatcher = GateHooks{}
	_ HookDispatcher          = (*hookdispatch.Dispatcher)(nil)
	_ ToolScheduler           = (*tooldispatch.Scheduler)(nil)
	_ PlanGate                = (*plangate.Gate)(nil)
)

// TestGateHooks_convertsOutcome asserts the plangate bridge passes a
// dispatch's verdict through unchanged. This is the integration point that
// lets internal/plangate keep declaring its own HookDispatcher instead of
// importing internal/hookdispatch.
func TestGateHooks_convertsOutcome(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	hooks := &fakeHooks{t: t, rec: rec}
	payload := &hookv1.HookPayload{Payload: &hookv1.HookPayload_PreToolCall{
		PreToolCall: &hookv1.PreToolCallPayload{Call: &toolv1.ToolCall{Id: "call-a"}},
	}}
	hooks.vetoPreToolCall = map[string]string{"call-a": "guard"}

	out, err := GateHooks{Dispatcher: hooks}.Dispatch(context.Background(), payload)
	if err != nil {
		t.Fatalf("Dispatch: unexpected error: %v", err)
	}
	if out.Decision != hookv1.HookDecision_HOOK_DECISION_DENY || out.DeniedBy != "guard" {
		t.Fatalf("outcome = %+v, want DENY by guard", out)
	}
	if out.Payload != payload {
		t.Fatalf("payload was not passed through")
	}
}

// TestGateHooks_errorIsNotAVerdict asserts a dispatcher-level failure
// reaches plangate as an error with a ZERO outcome — never an implicit
// allow or deny. plangate's own contract is that Decision is meaningless
// when err is non-nil, and manufacturing one here would persist a decision
// nobody made.
func TestGateHooks_errorIsNotAVerdict(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	hooks := &fakeHooks{t: t, rec: &recorder{}, errAt: map[string]error{"hooks:plan-ready": boom}}

	out, err := GateHooks{Dispatcher: hooks}.Dispatch(context.Background(), &hookv1.HookPayload{
		Payload: &hookv1.HookPayload_PlanReady{PlanReady: &hookv1.PlanReadyPayload{}},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Dispatch: got %v, want the dispatcher's error", err)
	}
	if out != (plangate.HookOutcome{}) {
		t.Fatalf("outcome = %+v, want the zero value alongside an error", out)
	}
}

// TestHookOutcome_isAStructConversion asserts every field survives the
// single Go struct conversion between the two field-for-field identical
// outcome types. If plangate.HookOutcome and hookdispatch.Outcome ever
// diverge, the conversion stops compiling — which is the intent.
func TestHookOutcome_isAStructConversion(t *testing.T) {
	t.Parallel()

	payload := &hookv1.HookPayload{Payload: &hookv1.HookPayload_PlanReady{PlanReady: &hookv1.PlanReadyPayload{}}}
	in := hookdispatch.Outcome{
		Payload:  payload,
		Decision: hookv1.HookDecision_HOOK_DECISION_DENY,
		DeniedBy: "policy",
	}
	got := hookOutcome(in)
	if got.Payload != payload || got.Decision != in.Decision || got.DeniedBy != in.DeniedBy {
		t.Fatalf("hookOutcome = %+v, want every field of %+v", got, in)
	}
}

// TestApplyOutcome_copiesOnlyWhatTheGateReads asserts the tooldispatch ->
// plangate conversion keeps the three fields the gate's ApplyResult needs
// and drops the two it has no place for.
func TestApplyOutcome_copiesOnlyWhatTheGateReads(t *testing.T) {
	t.Parallel()

	exit := int32(3)
	call := &toolv1.ToolCall{Id: "call-a"}
	res := &toolv1.ToolResult{}
	got := applyOutcome(tooldispatch.Outcome{Call: call, Result: res, ExitCode: &exit, Sequence: 42})

	if got.Call != call || got.Result != res || got.Error != nil {
		t.Fatalf("applyOutcome = %+v, want the call and result carried through", got)
	}
}

// TestResultText_handlesEmptyPayload asserts a result with no payload still
// renders as valid canonical JSON rather than an empty tool_result the model
// cannot interpret.
func TestResultText_handlesEmptyPayload(t *testing.T) {
	t.Parallel()

	if got := resultText(&toolv1.ToolResult{}); got != "{}" {
		t.Fatalf("resultText(empty) = %q, want %q", got, "{}")
	}
}

// TestDoneReason_String covers the label vocabulary a caller logs.
func TestDoneReason_String(t *testing.T) {
	t.Parallel()

	tests := map[DoneReason]string{
		DoneNone:         "none",
		DoneNoToolCalls:  "no_tool_calls",
		DoneTerminalTool: "terminal_tool",
		DoneReason(99):   "unknown",
	}
	for reason, want := range tests {
		if got := reason.String(); got != want {
			t.Errorf("DoneReason(%d).String() = %q, want %q", int(reason), got, want)
		}
	}
}
