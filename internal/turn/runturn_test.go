package turn

import (
	"context"
	"errors"
	"strings"
	"testing"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/callhash"
	"github.com/pluggableharness/agent/internal/contextassembly"
	"github.com/pluggableharness/agent/internal/providercatalog"
)

// TestRunTurn_doneCheckSkipsEverythingAfterStep6 locks in
// turn-algorithm.md#done-detection's MUST-support baseline: a message with
// no tool_use blocks ends the turn at step 6, and steps 7 through 15 never
// run at all.
func TestRunTurn_doneCheckSkipsEverythingAfterStep6(t *testing.T) {
	t.Parallel()

	msg := assistantMessage("msg-1", textBlock("nothing to do"))
	h := newHarness(t, response(msg))

	res, err := h.driver.RunTurn(context.Background(), baseRequest(nil))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if !res.Done || res.DoneReason != DoneNoToolCalls {
		t.Fatalf("Done/DoneReason = %v/%v, want true/%v", res.Done, res.DoneReason, DoneNoToolCalls)
	}
	if res.CallHashes != nil {
		t.Fatalf("CallHashes = %v, want nil for a turn with no tool calls", res.CallHashes)
	}

	want := []string{"context:assemble", "hooks:pre-model-call", "model:complete", "hooks:post-model-response"}
	if got := h.rec.snapshot(); !equalStrings(got, want) {
		t.Fatalf("call order mismatch\n got: %v\nwant: %v", got, want)
	}

	// History is history ++ message, with no tool_result message appended.
	if len(res.History) != 2 {
		t.Fatalf("History: got %d messages, want 2", len(res.History))
	}
	if res.History[1] != msg {
		t.Fatalf("History[1] is not the turn's own message")
	}
}

// TestRunTurn_terminatesTurnEndsTurnImmediately covers
// turn-algorithm.md#done-detection's opt-in explicit path: a successful call
// of a terminates_turn operation ends the turn right after its own
// post-tool-call hook, independent of other tool_use blocks in the same
// message — whose results still reach history, since dropping them would
// leave a tool_use block unanswered.
func TestRunTurn_terminatesTurnEndsTurnImmediately(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"task.finish": terminal(toolHandle("task", "finish", toolv1.ToolKind_TOOL_KIND_RESOURCE)),
		"fs.read":     toolHandle("fs", "read", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1",
		useBlock("call-a", "task.finish", map[string]any{"summary": "done"}),
		useBlock("call-b", "fs.read", map[string]any{"path": "a.txt"}),
	)

	h := newHarness(t, response(msg))
	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	if !res.Done || res.DoneReason != DoneTerminalTool {
		t.Fatalf("Done/DoneReason = %v/%v, want true/%v", res.Done, res.DoneReason, DoneTerminalTool)
	}
	if len(h.hooks.postTool) != 2 {
		t.Fatalf("post-tool-call dispatched %d times, want 2 — a terminal tool must not suppress its siblings' hooks", len(h.hooks.postTool))
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 2 {
		t.Fatalf("tool_result blocks: got %d, want 2 — every tool_use block must be answered", len(results))
	}
}

// TestRunTurn_terminatesTurnRequiresSuccess asserts the qualifier
// providercatalog.ToolHandle.TerminatesTurn's own doc comment states: it is
// a SUCCESSFUL call that ends the turn. A denied terminal tool leaves the
// loop running so the model can react to the denial.
func TestRunTurn_terminatesTurnRequiresSuccess(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"task.finish": terminal(toolHandle("task", "finish", toolv1.ToolKind_TOOL_KIND_RESOURCE)),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "task.finish", map[string]any{}))

	h := newHarness(t, response(msg))
	h.gate.denyPlan = map[string]bool{"call-a": true}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if res.Done {
		t.Fatalf("Done = true for a DENIED terminal tool, want false")
	}
}

// TestRunTurn_toolSpecWithholding covers both schema-removal mechanisms:
// plan mode drops only resource specs
// (plan-apply-gate.md#decision-semantics), the limit-reached final-answer
// turn drops every spec and appends the synthetic instruction
// (turn-algorithm.md#limit-reached-behavior). Neither is a runtime
// interception — the model simply never sees the tool.
func TestRunTurn_toolSpecWithholding(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file":  toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
	}

	tests := []struct {
		name        string
		planMode    bool
		finalAnswer bool
		wantTools   []string
		wantMessage bool
	}{
		{name: "unrestricted", wantTools: []string{"fs.read_file", "fs.write_file"}},
		{name: "plan mode drops resource specs", planMode: true, wantTools: []string{"fs.read_file"}},
		{name: "final answer drops every spec", finalAnswer: true, wantTools: nil, wantMessage: true},
		{name: "final answer wins over plan mode", planMode: true, finalAnswer: true, wantTools: nil, wantMessage: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, response(assistantMessage("msg-1", textBlock("ok"))))
			req := baseRequest(tools)
			req.PlanMode = tc.planMode
			req.FinalAnswer = tc.finalAnswer
			req.FinalAnswerReason = "error_max_turns"

			if _, err := h.driver.RunTurn(context.Background(), req); err != nil {
				t.Fatalf("RunTurn: unexpected error: %v", err)
			}

			got := make([]string, 0, 2)
			for _, decl := range h.model.requests[0].Request.GetTools() {
				got = append(got, decl.GetName())
			}
			if !equalStrings(got, tc.wantTools) {
				t.Fatalf("tool declarations: got %v, want %v", got, tc.wantTools)
			}

			// The hook sees the same messages the request carries, so
			// asserting on the payload also asserts on the wire request.
			messages := h.hooks.preModel[0].GetMessages()
			last := messages[len(messages)-1].GetContent()[0].GetText().GetText()
			hasInstruction := strings.Contains(last, "error_max_turns")
			if hasInstruction != tc.wantMessage {
				t.Fatalf("synthetic final-answer instruction present = %v, want %v (last message %q)", hasInstruction, tc.wantMessage, last)
			}
		})
	}
}

// TestRunTurn_preToolCallVetoRemovesCall covers step 7's veto branch: a DENY
// removes the call from consideration entirely — it never reaches a
// precheck, a plan, or a scheduler — and synthesizes the tool_result denial
// the model observes in its own history.
func TestRunTurn_preToolCallVetoRemovesCall(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
		"fs.read_file":  toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1",
		useBlock("call-a", "fs.write_file", map[string]any{"path": "b.txt"}),
		useBlock("call-b", "fs.read_file", map[string]any{"path": "a.txt"}),
	)

	h := newHarness(t, response(msg))
	h.hooks.vetoPreToolCall = map[string]string{"call-a": "guard"}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	// The vetoed resource call was the only one, so no plan was built at
	// all — it was removed from consideration, not denied by the gate.
	if len(h.gate.built) != 0 {
		t.Fatalf("gate.Build called %d times, want 0 — a vetoed call must never reach the plan", len(h.gate.built))
	}
	for _, batch := range h.tools.executed {
		for _, c := range batch {
			if c.Call.GetId() == "call-a" {
				t.Fatalf("vetoed call reached the scheduler")
			}
		}
	}

	results := toolResultTexts(res.History[2])
	if len(results) != 2 {
		t.Fatalf("tool_result blocks: got %d, want 2", len(results))
	}
	denial := results[0]
	if denial.ToolUseID != "call-a" || !denial.IsError {
		t.Fatalf("denial block: got %+v, want call-a with is_error", denial)
	}
	want := "fs.write_file was denied (hook-veto:guard); this call was not executed"
	if denial.Text != want {
		t.Fatalf("denial text:\n got %q\nwant %q", denial.Text, want)
	}
}

// TestRunTurn_historyPairsResultsInDeclarationOrder is step 13's and step
// 15's shared invariant: outcomes are reported and rendered in the order
// their tool_use blocks appeared, never grouped by kind and never in
// completion order. Every vendor API depends on that pairing.
func TestRunTurn_historyPairsResultsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
		"fs.read_file":  toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
		"ui.ask":        toolHandle("ui", "ask", toolv1.ToolKind_TOOL_KIND_INTERACTIVE),
	}
	// Deliberately interleaved: resource, interactive, data_source,
	// resource. Grouping by kind would reorder every one of them.
	msg := assistantMessage("msg-1",
		useBlock("call-1", "fs.write_file", map[string]any{"path": "1"}),
		useBlock("call-2", "ui.ask", map[string]any{"q": "2"}),
		useBlock("call-3", "fs.read_file", map[string]any{"path": "3"}),
		useBlock("call-4", "fs.write_file", map[string]any{"path": "4"}),
	)

	h := newHarness(t, response(msg))
	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	wantOrder := []string{"call-1", "call-2", "call-3", "call-4"}

	got := make([]string, 0, 4)
	for _, p := range h.hooks.postTool {
		got = append(got, p.GetCall().GetId())
	}
	if !equalStrings(got, wantOrder) {
		t.Fatalf("post-tool-call order: got %v, want %v", got, wantOrder)
	}

	got = got[:0]
	for _, view := range toolResultTexts(res.History[2]) {
		got = append(got, view.ToolUseID)
	}
	if !equalStrings(got, wantOrder) {
		t.Fatalf("tool_result order: got %v, want %v", got, wantOrder)
	}
}

// TestRunTurn_callHashes asserts the doom-loop hashes the session driver
// feeds into its step-16 check: one per resource and data_source call, in
// declaration order, computed by internal/callhash over the scoped
// "<provider>.<tool>" name. Interactive calls contribute none —
// turn-algorithm.md#doom-loop-detection scopes the window to
// resource/data-source calls.
func TestRunTurn_callHashes(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
		"fs.read_file":  toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
		"ui.ask":        toolHandle("ui", "ask", toolv1.ToolKind_TOOL_KIND_INTERACTIVE),
	}
	writeArgs := map[string]any{"path": "b.txt"}
	readArgs := map[string]any{"path": "a.txt"}
	msg := assistantMessage("msg-1",
		useBlock("call-1", "fs.write_file", writeArgs),
		useBlock("call-2", "ui.ask", map[string]any{"q": "?"}),
		useBlock("call-3", "fs.read_file", readArgs),
	)

	h := newHarness(t, response(msg))
	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	want := []string{
		callhash.Call("fs.write_file", mustStruct(writeArgs)),
		callhash.Call("fs.read_file", mustStruct(readArgs)),
	}
	if !equalStrings(res.CallHashes, want) {
		t.Fatalf("CallHashes:\n got %v\nwant %v", res.CallHashes, want)
	}
}

// TestRunTurn_cancellationPropagatesUnwrapped covers .claude/rules/grpc.md's
// "cancellation is normal control flow": a ctx canceled mid-turn surfaces as
// a bare ctx.Err(), not wrapped in this package's own error string.
func TestRunTurn_cancellationPropagatesUnwrapped(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Cancel from inside the chain, then fail the dispatch exactly as a
	// real dispatcher does when it observes a canceled parent.
	h.hooks.onDispatch = func(label string) {
		if label == "hooks:pre-tool-call" {
			cancel()
		}
	}
	h.hooks.errAt = map[string]error{"hooks:pre-tool-call": context.Canceled}

	_, err := h.driver.RunTurn(ctx, baseRequest(tools))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunTurn: got %v, want context.Canceled", err)
	}
	if err.Error() != context.Canceled.Error() {
		t.Fatalf("RunTurn: error was wrapped (%q); cancellation must surface bare", err)
	}
}

// TestRunTurn_precheckDenialSkipsScheduler covers step 9's denial branch:
// plan-apply-gate.md#data-source-and-interactive-calls makes a denied read
// synthesize its own tool_result and never execute, and it makes a tripped
// circuit breaker something the caller is told about rather than something
// this package acts on.
func TestRunTurn_precheckDenialSkipsScheduler(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	h.gate.denyPrecheck = map[string]bool{"call-a": true}
	h.gate.trippedPrecheck = map[string]bool{"call-a": true}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if len(h.tools.executed) != 0 {
		t.Fatalf("scheduler ran %d batches, want 0 — a denied call must never execute", len(h.tools.executed))
	}
	if !equalStrings(res.TrippedProviders, []string{"fs"}) {
		t.Fatalf("TrippedProviders = %v, want [fs]", res.TrippedProviders)
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("tool_result blocks: got %+v, want one denial", results)
	}
}

// TestRunTurn_schedulerCrashTripSurfacesAsTrippedProvider covers the OTHER
// half of the circuit breaker. One per-session *circuitbreaker.Breaker is
// shared by the plan gate (which records denials) and the tool scheduler
// (which records plugin crashes); the denial half already reaches
// Result.TrippedProviders through Precheck and the plan gate, but the
// crash half arrives inside tooldispatch.Outcome.Error.Details'
// BreakerTrippedDetail boolean and used to be read by nobody at all.
//
// The consequence of not reading it was that a repeatedly-crashing tool
// provider tripped its breaker and the session never noticed, re-calling
// the crashing provider every turn until an ordinary bound fired — the
// exact wall plan-apply-gate.md#circuit-breaker-on-repeated-denials
// exists to stop the loop running into.
func TestRunTurn_schedulerCrashTripSurfacesAsTrippedProvider(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	h.tools.crashTripCalls = map[string]bool{"call-a": true}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if !equalStrings(res.TrippedProviders, []string{"fs"}) {
		t.Fatalf("TrippedProviders = %v, want [fs] — a crash-driven breaker trip must reach the caller", res.TrippedProviders)
	}
	// The crash is still an ordinary per-call failure: it resolves to an
	// error tool_result rather than failing the turn.
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("tool_result blocks: got %+v, want one error result", results)
	}
}

// TestRunTurn_breakerTripRequiresTheCrashedCategory pins the category
// guard on the trip reader. The contract it implements is specifically
// "a TOOL_ERROR_CATEGORY_PROCESS_CRASHED error whose crash tripped the
// breaker" (tooldispatch.Outcome.Error's doc comment), so the Details flag
// alone is not sufficient evidence.
//
// internal/tooldispatch cannot currently produce this shape — recordBreaker
// stamps BreakerTrippedDetail only inside its crashed branch — which is
// exactly why the guard needs a test: without one, nothing would notice a
// future writer reusing the key on another category and silently routing
// ordinary timeouts through the session's limit-reached path.
func TestRunTurn_breakerTripRequiresTheCrashedCategory(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	h.tools.mislabeledTripCalls = map[string]bool{"call-a": true}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if len(res.TrippedProviders) != 0 {
		t.Fatalf("TrippedProviders = %v, want empty: the trip signal is only meaningful on a process_crashed outcome", res.TrippedProviders)
	}
	// It is still an ordinary failed call — the guard suppresses the trip
	// routing, never the tool_result the model sees.
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("tool_result blocks: got %+v, want one error result", results)
	}
}

// TestRunTurn_schedulerSuccessLeavesNoTrippedProvider is the negative
// control: an ordinary successful call must not report a trip, so the
// check above cannot pass by reporting every provider it schedules.
func TestRunTurn_schedulerSuccessLeavesNoTrippedProvider(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if len(res.TrippedProviders) != 0 {
		t.Fatalf("TrippedProviders = %v, want empty for a healthy call", res.TrippedProviders)
	}
}

// TestRunTurn_planDenialReusesGateBlocks asserts a plan-gate denial reaches
// the model as plangate.DenialBlocks' own block, verbatim — one denial
// vocabulary, not a second rendering of the same verdict — while still
// landing in the denied call's declaration slot.
func TestRunTurn_planDenialReusesGateBlocks(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
	}
	msg := assistantMessage("msg-1",
		useBlock("call-a", "fs.write_file", map[string]any{"path": "a"}),
		useBlock("call-b", "fs.write_file", map[string]any{"path": "b"}),
	)

	h := newHarness(t, response(msg))
	h.gate.denyPlan = map[string]bool{"call-a": true}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	results := toolResultTexts(res.History[2])
	if len(results) != 2 || results[0].ToolUseID != "call-a" || results[1].ToolUseID != "call-b" {
		t.Fatalf("tool_result order: got %+v, want call-a then call-b", results)
	}
	want := "fs.write_file was denied (policy:default); this call was not executed"
	if results[0].Text != want || !results[0].IsError {
		t.Fatalf("denial block:\n got %+v\nwant text %q with is_error", results[0], want)
	}
	if results[1].IsError {
		t.Fatalf("allowed call's result marked is_error: %+v", results[1])
	}

	// Only the allowed item was applied, and the gate's ApplyResult saw
	// exactly that one outcome.
	if len(h.gate.applied) != 1 || len(h.gate.applied[0]) != 1 {
		t.Fatalf("apply outcomes: got %v, want exactly one", h.gate.applied)
	}
	if h.gate.applied[0][0].Call.GetId() != "call-b" {
		t.Fatalf("applied outcome is for %q, want call-b", h.gate.applied[0][0].Call.GetId())
	}
}

// TestRunTurn_provisionalItemCarriedForwardByIdentity asserts step 7's
// provisional PlanItem is the very pointer step 10 hands to the gate — never
// re-minted — which is what lets a decision stamped at step 11 be visible on
// the item a hook already saw.
func TestRunTurn_provisionalItemCarriedForwardByIdentity(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.write_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	if _, err := h.driver.RunTurn(context.Background(), baseRequest(tools)); err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	hooked := h.hooks.preTool[0].GetPlanItem()
	built := h.gate.built[0].Items[0].Item
	if hooked != built {
		t.Fatalf("plan item was re-minted between step 7 and step 10")
	}
	if hooked.GetKind() != toolv1.ToolKind_TOOL_KIND_RESOURCE {
		t.Fatalf("plan item kind = %v, want TOOL_KIND_RESOURCE snapshot", hooked.GetKind())
	}
	if hooked.GetDescription() != "write_file description" {
		t.Fatalf("plan item description = %q, want the schema snapshot", hooked.GetDescription())
	}
	// The hook saw it PENDING; the gate stamped it afterward.
	if built.GetDecision() != planv1.PlanDecision_PLAN_DECISION_ALLOW {
		t.Fatalf("plan item decision after Decide = %v, want ALLOW", built.GetDecision())
	}
}

// TestRunTurn_outOfScopeToolNeverDispatches asserts a tool_use block naming
// an operation this turn never offered resolves to an error tool_result in
// its own slot, without reaching a hook, a gate, or a scheduler — there is
// no handle to snapshot a plan item from.
func TestRunTurn_outOfScopeToolNeverDispatches(t *testing.T) {
	t.Parallel()

	msg := assistantMessage("msg-1", useBlock("call-a", "fs.delete_everything", map[string]any{}))
	h := newHarness(t, response(msg))

	res, err := h.driver.RunTurn(context.Background(), baseRequest(nil))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if len(h.hooks.preTool) != 0 {
		t.Fatalf("pre-tool-call dispatched for an out-of-scope tool")
	}
	if len(h.hooks.postTool) != 0 {
		t.Fatalf("post-tool-call dispatched for a call that was never built")
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Text, "not in scope") {
		t.Fatalf("tool_result blocks: got %+v, want one out-of-scope error", results)
	}
}

// TestRunTurn_unspecifiedKindIsDenied covers splitByKind's default branch: an
// operation whose schema declares no kind cannot be gated, since
// plan-apply-gate.md's whole decision structure is kind-driven, so it is
// denied rather than executed under a guessed classification.
func TestRunTurn_unspecifiedKindIsDenied(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"odd.thing": toolHandle("odd", "thing", toolv1.ToolKind_TOOL_KIND_UNSPECIFIED),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "odd.thing", map[string]any{}))

	h := newHarness(t, response(msg))
	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if len(h.tools.executed) != 0 || len(h.gate.built) != 0 {
		t.Fatalf("an unspecified-kind call reached a gate or scheduler")
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("tool_result blocks: got %+v, want one error", results)
	}
	if res.CallHashes != nil {
		t.Fatalf("CallHashes = %v, want nil for a call of no gateable kind", res.CallHashes)
	}
}

// TestRunTurn_compactorRewrittenHistoryReplacesHistory covers
// context/protocol.md#session-wide-conversation-compaction's MUST: when a
// compactor returns a rewritten history, the kernel replaces the turn's
// conversation history with it before the model call — and the replacement
// is durable, so it is what the next turn carries too.
func TestRunTurn_compactorRewrittenHistoryReplacesHistory(t *testing.T) {
	t.Parallel()

	rewritten := []*contentv1.Message{userMessage("compacted summary")}
	h := newHarness(t, response(assistantMessage("msg-1", textBlock("ok"))))
	h.context.res = contextassembly.Result{RewrittenHistory: rewritten, AssembledTokensLastTurn: 42}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(nil))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	sent := h.model.requests[0].Request.GetMessages()
	if len(sent) != 1 || sent[0] != rewritten[0] {
		t.Fatalf("request messages: got %d, want the compactor's rewritten history", len(sent))
	}
	if len(res.History) != 2 || res.History[0] != rewritten[0] {
		t.Fatalf("Result.History does not start from the rewritten history")
	}
	if res.AssembledTokens != 42 {
		t.Fatalf("AssembledTokens = %d, want 42 threaded from the assembler", res.AssembledTokens)
	}
}

// TestRunTurn_preModelCallTransformIsRequestScoped records this package's
// one genuine judgment call about the pre-model-call hook: its
// transform-mutable messages rewrite what the model is SENT, and do not
// rewrite the durable conversation history the next turn carries. See
// CLAUDE.md.
func TestRunTurn_preModelCallTransformIsRequestScoped(t *testing.T) {
	t.Parallel()

	redacted := []*contentv1.Message{userMessage("REDACTED")}
	h := newHarness(t, response(assistantMessage("msg-1", textBlock("ok"))))
	h.hooks.transformMessages = redacted

	req := baseRequest(nil)
	res, err := h.driver.RunTurn(context.Background(), req)
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	if sent := h.model.requests[0].Request.GetMessages(); len(sent) != 1 || sent[0] != redacted[0] {
		t.Fatalf("request messages did not take the transform")
	}
	if res.History[0] != req.History[0] {
		t.Fatalf("Result.History took the pre-model-call transform; it must stay request-scoped")
	}
}

// TestRunTurn_schedulerOutcomeCountMismatch asserts the contract guard: a
// scheduler returning a different number of outcomes than calls would pair
// a result with the wrong tool_use block, so the turn fails loudly instead.
func TestRunTurn_schedulerOutcomeCountMismatch(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	h.tools.shortOutcomes = true

	_, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if !errors.Is(err, ErrOutcomeCount) {
		t.Fatalf("RunTurn: got %v, want ErrOutcomeCount", err)
	}
}

// TestRunTurn_collaboratorErrorsPropagate walks every step that can fail
// and asserts the failure reaches the caller wrapped with this package's
// own prefix rather than being swallowed into a half-run turn.
func TestRunTurn_collaboratorErrorsPropagate(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	tools := map[string]providercatalog.ToolHandle{
		"fs.write_file": toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.write_file", map[string]any{"path": "a"}))

	tests := []struct {
		name   string
		script func(*harness)
		want   string
	}{
		{name: "context-assemble", script: func(h *harness) { h.context.err = boom }, want: "turn: context-assemble"},
		{name: "pre-model-call", script: func(h *harness) { h.hooks.errAt = map[string]error{"hooks:pre-model-call": boom} }, want: "turn: pre-model-call"},
		{name: "model call", script: func(h *harness) { h.model.err = boom }, want: "turn: model call"},
		{name: "post-model-response", script: func(h *harness) {
			h.hooks.errAt = map[string]error{"hooks:post-model-response": boom}
		}, want: "turn: post-model-response"},
		{name: "pre-tool-call", script: func(h *harness) { h.hooks.errAt = map[string]error{"hooks:pre-tool-call": boom} }, want: "turn: pre-tool-call"},
		{name: "build plan", script: func(h *harness) { h.gate.errBuild = boom }, want: "turn: build plan"},
		{name: "plan-ready", script: func(h *harness) { h.gate.errDecide = boom }, want: "turn: plan-ready"},
		{name: "execute", script: func(h *harness) { h.tools.errExecute = boom }, want: "turn: execute tool calls"},
		{name: "post-tool-call", script: func(h *harness) { h.hooks.errAt = map[string]error{"hooks:post-tool-call": boom} }, want: "turn: post-tool-call"},
		{name: "apply result", script: func(h *harness) { h.gate.errResult = boom }, want: "turn: apply result"},
		{name: "post-apply", script: func(h *harness) { h.hooks.errAt = map[string]error{"hooks:post-apply": boom} }, want: "turn: post-apply"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, response(msg))
			tc.script(h)

			_, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
			if err == nil {
				t.Fatalf("RunTurn: got nil error, want one from %s", tc.name)
			}
			if !errors.Is(err, boom) {
				t.Fatalf("RunTurn: %v does not wrap the collaborator's error", err)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("RunTurn: error %q does not start with %q", err, tc.want)
			}
		})
	}
}

// TestRunTurn_failedToolResultReachesHistory asserts a tool that fails at
// execution surfaces to the model as an is_error tool_result, the same
// single channel a denial travels on.
func TestRunTurn_failedToolResultReachesHistory(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	h.tools.failCalls = map[string]bool{"call-a": true}

	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || !results[0].IsError || results[0].Text != "read_file failed" {
		t.Fatalf("tool_result blocks: got %+v, want the failure text with is_error", results)
	}
	if h.hooks.postTool[0].GetError() == nil {
		t.Fatalf("post-tool-call carried no error outcome for a failed call")
	}
}

// TestRunTurn_successfulResultTextIsCanonicalJSON asserts the model reads a
// tool result as its payload's canonical encoding — the one deterministic
// structpb encoding in the codebase, since a tool_result block lands in
// persisted history.
func TestRunTurn_successfulResultTextIsCanonicalJSON(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	res, err := h.driver.RunTurn(context.Background(), baseRequest(tools))
	if err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 1 || results[0].Text != `{"ok":"read_file"}` {
		t.Fatalf("tool_result text: got %+v, want canonical JSON", results)
	}
}

// TestRunTurn_contextInputsThreaded asserts every TurnInputs field the
// assembler cannot invent for itself arrives from the request.
func TestRunTurn_contextInputsThreaded(t *testing.T) {
	t.Parallel()

	h := newHarness(t, response(assistantMessage("msg-1", textBlock("ok"))))
	req := baseRequest(nil)
	req.ParentSessionID = "parent-1"
	req.FilesTouched = []string{"a.txt"}
	req.AssembledTokensLastTurn = 99

	if _, err := h.driver.RunTurn(context.Background(), req); err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	in := h.context.inputs[0]
	switch {
	case in.SessionID != "sess-1", in.ParentSessionID != "parent-1", in.TurnID != "turn-1":
		t.Fatalf("identity fields not threaded: %+v", in)
	case in.WorkingDirectory != "/work", in.AssembledTokensLastTurn != 99:
		t.Fatalf("turn fields not threaded: %+v", in)
	case in.ModelTarget == nil || in.ModelTarget.GetId() != "test-model":
		t.Fatalf("model target not threaded: %+v", in.ModelTarget)
	case len(in.FilesTouched) != 1 || in.FilesTouched[0] != "a.txt":
		t.Fatalf("files touched not threaded: %+v", in.FilesTouched)
	}
}

// TestRunTurn_callContextStamped asserts every tool call carries the
// session/turn/working-directory attribution a plugin echoes back on its own
// kernel callbacks.
func TestRunTurn_callContextStamped(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
	}
	msg := assistantMessage("msg-1", useBlock("call-a", "fs.read_file", map[string]any{"path": "a"}))

	h := newHarness(t, response(msg))
	if _, err := h.driver.RunTurn(context.Background(), baseRequest(tools)); err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}
	call := h.tools.executed[0][0].Call
	if call.GetToolName() != "read_file" {
		t.Fatalf("ToolCall.tool_name = %q, want the provider-local schema name", call.GetToolName())
	}
	cc := call.GetCallContext()
	if cc.GetSessionId() != "sess-1" || cc.GetTurnId() != "turn-1" || cc.GetWorkingDirectory() != "/work" {
		t.Fatalf("call context = %+v, want the request's own", cc)
	}
}

// TestRunTurn_rejectsIncompleteRequest asserts the three request fields only
// a session driver can supply are required rather than guessed.
func TestRunTurn_rejectsIncompleteRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Request)
		want   error
	}{
		{name: "no session id", mutate: func(r *Request) { r.SessionID = "" }, want: ErrNoSessionID},
		{name: "no turn id", mutate: func(r *Request) { r.TurnID = "" }, want: ErrNoTurnID},
		{name: "no model target", mutate: func(r *Request) { r.ModelTarget = nil }, want: ErrNoModelTarget},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			req := baseRequest(nil)
			tc.mutate(&req)
			if _, err := h.driver.RunTurn(context.Background(), req); !errors.Is(err, tc.want) {
				t.Fatalf("RunTurn: got %v, want %v", err, tc.want)
			}
			if got := h.rec.snapshot(); len(got) != 0 {
				t.Fatalf("collaborators were called for an invalid request: %v", got)
			}
		})
	}
}
