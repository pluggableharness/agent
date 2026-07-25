package turn

import (
	"context"
	"testing"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
)

// TestRunTurn_executesStepsInDocumentedOrder is this package's conformance
// test — the one that proves
// docs/specifications/agent-loop/conformance.md's first MUST, "turn
// algorithm executes steps in the documented order"
// (turn-algorithm.md#the-runturn-algorithm).
//
// It runs a realistic two-turn scenario through hand-written fakes that each
// append their own name to one shared ordered log, and asserts that log
// matches the numbered algorithm exactly:
//
//	turn 1  (model asks for one data_source and one resource call, both approved)
//	  1.  context-assemble        -> context:assemble
//	  2.  pre-model-call          -> hooks:pre-model-call
//	  3.  StreamCompletion   \
//	  4.  accumulate          }   -> model:complete
//	  5.  post-model-response     -> hooks:post-model-response
//	  6.  DoneCheck               (implicit; two tool_use blocks, so not done)
//	  7.  pre-tool-call x2        -> hooks:pre-tool-call, hooks:pre-tool-call
//	  8.  split_by_kind           (pure Go, no collaborator to record)
//	  9.  precheck + execute      -> gate:precheck, tools:execute
//	  9b. (no interactive calls this turn)
//	  10. build_plan              -> gate:build
//	  11. plan-ready              -> gate:decide (dispatches the chain itself)
//	  12. apply                   -> tools:execute
//	  13. post-tool-call x2       -> hooks:post-tool-call, hooks:post-tool-call
//	  14. post-apply              -> gate:result, hooks:post-apply
//	  15. history append          (pure Go, asserted on the Result below)
//
//	turn 2  (model answers with no tool calls: steps 1-6 only, 7-15 skipped)
//	  1.  context-assemble        -> context:assemble
//	  2.  pre-model-call          -> hooks:pre-model-call
//	  3-4.                        -> model:complete
//	  5.  post-model-response     -> hooks:post-model-response
//	  6.  DoneCheck               -> done
//
// Steps 16-18 (doom-loop, bounds, the loop itself) are deliberately absent:
// they belong to the session driver that calls RunTurn, which is why turn 1
// hands back CallHashes and turn 2 hands back Done.
func TestRunTurn_executesStepsInDocumentedOrder(t *testing.T) {
	t.Parallel()

	read := toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE)
	write := toolHandle("fs", "write_file", toolv1.ToolKind_TOOL_KIND_RESOURCE)
	tools := map[string]providercatalog.ToolHandle{"fs.read_file": read, "fs.write_file": write}

	first := assistantMessage("msg-1",
		textBlock("working on it"),
		useBlock("call-a", "fs.read_file", map[string]any{"path": "a.txt"}),
		useBlock("call-b", "fs.write_file", map[string]any{"path": "b.txt", "body": "x"}),
	)
	second := assistantMessage("msg-2", textBlock("all done"))

	h := newHarness(t, response(first), response(second))

	req := baseRequest(tools)
	res, err := h.driver.RunTurn(context.Background(), req)
	if err != nil {
		t.Fatalf("RunTurn (turn 1): unexpected error: %v", err)
	}
	if res.Done {
		t.Fatalf("RunTurn (turn 1): Done = true, want false — the model asked for tool calls")
	}

	next := req
	next.TurnID = "turn-2"
	next.TurnIndex = 1
	next.History = res.History
	next.AssembledTokensLastTurn = res.AssembledTokens
	final, err := h.driver.RunTurn(context.Background(), next)
	if err != nil {
		t.Fatalf("RunTurn (turn 2): unexpected error: %v", err)
	}
	if !final.Done || final.DoneReason != DoneNoToolCalls {
		t.Fatalf("RunTurn (turn 2): Done = %v/%v, want true/%v", final.Done, final.DoneReason, DoneNoToolCalls)
	}

	want := []string{
		// Turn 1.
		"context:assemble",          // 1
		"hooks:pre-model-call",      // 2
		"model:complete",            // 3-4
		"hooks:post-model-response", // 5
		"hooks:pre-tool-call",       // 7 (fs.read_file)
		"hooks:pre-tool-call",       // 7 (fs.write_file)
		"gate:precheck",             // 9
		"tools:execute",             // 9
		"gate:build",                // 10
		"gate:decide",               // 11
		"tools:execute",             // 12
		"hooks:post-tool-call",      // 13 (fs.read_file)
		"hooks:post-tool-call",      // 13 (fs.write_file)
		"gate:result",               // 14
		"hooks:post-apply",          // 14
		// Turn 2.
		"context:assemble",          // 1
		"hooks:pre-model-call",      // 2
		"model:complete",            // 3-4
		"hooks:post-model-response", // 5
	}
	if got := h.rec.snapshot(); !equalStrings(got, want) {
		t.Fatalf("call order mismatch\n got: %v\nwant: %v", got, want)
	}

	// Step 15's own product, which has no collaborator to record: the
	// history the next turn carries.
	if len(res.History) != 3 {
		t.Fatalf("turn 1 History: got %d messages, want 3 (prior + assistant + tool results)", len(res.History))
	}
	results := toolResultTexts(res.History[2])
	if len(results) != 2 || results[0].ToolUseID != "call-a" || results[1].ToolUseID != "call-b" {
		t.Fatalf("turn 1 tool_result blocks: got %+v, want call-a then call-b", results)
	}
}

// TestRunTurn_step9bRunsInteractiveSequentially covers the one branch the
// two-turn conformance scenario above does not reach: step 9b's interactive
// group, prechecked exactly like step 9's data_source group but handed to
// the strictly sequential scheduler path
// (plan-apply-gate.md#data-source-and-interactive-calls).
func TestRunTurn_step9bRunsInteractiveSequentially(t *testing.T) {
	t.Parallel()

	tools := map[string]providercatalog.ToolHandle{
		"fs.read_file": toolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE),
		"ui.ask":       toolHandle("ui", "ask", toolv1.ToolKind_TOOL_KIND_INTERACTIVE),
	}
	msg := assistantMessage("msg-1",
		useBlock("call-a", "fs.read_file", map[string]any{"path": "a.txt"}),
		useBlock("call-b", "ui.ask", map[string]any{"question": "ok?"}),
	)

	h := newHarness(t, response(msg))
	if _, err := h.driver.RunTurn(context.Background(), baseRequest(tools)); err != nil {
		t.Fatalf("RunTurn: unexpected error: %v", err)
	}

	want := []string{
		"context:assemble",
		"hooks:pre-model-call",
		"model:complete",
		"hooks:post-model-response",
		"hooks:pre-tool-call",
		"hooks:pre-tool-call",
		"gate:precheck",             // 9 — data_source
		"tools:execute",             // 9 — concurrent
		"gate:precheck",             // 9b — interactive, same precheck
		"tools:execute-interactive", // 9b — sequential, never Execute
		"hooks:post-tool-call",
		"hooks:post-tool-call",
	}
	if got := h.rec.snapshot(); !equalStrings(got, want) {
		t.Fatalf("call order mismatch\n got: %v\nwant: %v", got, want)
	}

	// No resource calls this turn, so no plan was built, decided, or
	// applied — the log above already proves it, and this asserts the
	// reason rather than the symptom.
	if len(h.gate.built) != 0 {
		t.Fatalf("gate.Build called %d times for a turn with no resource calls, want 0", len(h.gate.built))
	}
}
