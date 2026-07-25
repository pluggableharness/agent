package plangate

import (
	"context"
	"errors"
	"testing"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/policy"
	"github.com/pluggableharness/agent/internal/statebackend"
)

// decidedThreeProviderPlan runs a plan through Decide with fs allowed,
// http failing-but-allowed, and shell denied, returning the decision set
// Result is then exercised against.
func decidedThreeProviderPlan(t *testing.T, sink *recordingSink) (*Gate, Decisions) {
	t.Helper()

	g := newTestGate(t, Config{
		Rules: []policy.Rule{
			ruleFor("allow-writes", "fs", "write_file", policy.ActionAllow),
			ruleFor("allow-post", "http", "post", policy.ActionAllow),
			ruleFor("deny-shell", "shell", "exec", policy.ActionDeny),
		},
		Events: sink,
	})
	d, err := g.Decide(context.Background(), threeProviderPlan())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return g, d
}

func TestResult_assemblesEveryOutcomeInPlanOrder(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	g, d := decidedThreeProviderPlan(t, sink)

	result, err := g.Result(context.Background(), "turn-1", d, []ApplyOutcome{
		// Deliberately out of plan order: the persisted record must not
		// depend on which call finished first.
		{Call: &toolv1.ToolCall{Id: "call-i2"}, Error: &toolv1.ToolError{
			Category: toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_EXECUTION_FAILED,
			Message:  "502",
		}},
		{Call: &toolv1.ToolCall{Id: "call-i1"}, Result: &toolv1.ToolResult{}},
	})
	if err != nil {
		t.Fatalf("Result: %v", err)
	}

	if result.GetTurnId() != "turn-1" {
		t.Errorf("turn id = %q, want %q", result.GetTurnId(), "turn-1")
	}
	items := result.GetItems()
	if len(items) != 3 {
		t.Fatalf("apply items = %d, want 3", len(items))
	}

	wantOutcome := []planv1.ApplyResult_ApplyOutcome{
		planv1.ApplyResult_APPLY_OUTCOME_APPLIED,
		planv1.ApplyResult_APPLY_OUTCOME_FAILED,
		planv1.ApplyResult_APPLY_OUTCOME_DENIED,
	}
	wantPlanItem := []string{"i1", "i2", "i3"}
	for i, item := range items {
		if item.GetOutcome() != wantOutcome[i] {
			t.Errorf("item %d outcome = %v, want %v", i, item.GetOutcome(), wantOutcome[i])
		}
		if item.GetPlanItemId() != wantPlanItem[i] {
			t.Errorf("item %d plan_item_id = %q, want %q", i, item.GetPlanItemId(), wantPlanItem[i])
		}
		if item.GetCallId() != "call-"+wantPlanItem[i] {
			t.Errorf("item %d call_id = %q", i, item.GetCallId())
		}
	}
	if items[0].GetToolResult() == nil {
		t.Error("an applied item carries no ToolResult")
	}
	if items[1].GetToolError() == nil {
		t.Error("a failed item carries no ToolError")
	}
	if items[2].GetToolResult() != nil || items[2].GetToolError() != nil {
		t.Error("a denied item carries a result; neither outcome executes the call")
	}

	// One apply event, kernel-produced.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("AppendEvent calls = %d, want exactly 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Kind != kernelv1.EventKind_EVENT_KIND_APPLY {
		t.Errorf("event kind = %v, want EVENT_KIND_APPLY", ev.Kind)
	}
	if !statebackend.IsKernelProducer(ev.Producer) {
		t.Errorf("event producer = %v, want the reserved kernel producer", ev.Producer)
	}
	if len(ev.Payload) == 0 {
		t.Error("apply event payload is empty")
	}
}

func TestResult_fullyDeniedPlanNeedsNoOutcomes(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	g := newTestGate(t, Config{Hooks: vetoHooks("guardrails"), Events: sink})
	d, err := g.Decide(context.Background(), threeProviderPlan())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	result, err := g.Result(context.Background(), "turn-1", d, nil)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	for _, item := range result.GetItems() {
		if item.GetOutcome() != planv1.ApplyResult_APPLY_OUTCOME_DENIED {
			t.Errorf("item %s outcome = %v, want DENIED", item.GetPlanItemId(), item.GetOutcome())
		}
	}
}

func TestResult_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  []ApplyOutcome
		want error
	}{
		{
			name: "an allowed item with no outcome",
			out:  []ApplyOutcome{{Call: &toolv1.ToolCall{Id: "call-i1"}, Result: &toolv1.ToolResult{}}},
			want: ErrMissingOutcome,
		},
		{
			name: "an outcome matching no item",
			out: []ApplyOutcome{
				{Call: &toolv1.ToolCall{Id: "call-i1"}, Result: &toolv1.ToolResult{}},
				{Call: &toolv1.ToolCall{Id: "call-i2"}, Result: &toolv1.ToolResult{}},
				{Call: &toolv1.ToolCall{Id: "call-ghost"}, Result: &toolv1.ToolResult{}},
			},
			want: ErrUnmatchedOutcome,
		},
		{
			name: "an outcome carrying neither result nor error",
			out:  []ApplyOutcome{{Call: &toolv1.ToolCall{Id: "call-i1"}}},
			want: ErrInvalidOutcome,
		},
		{
			name: "an outcome carrying both result and error",
			out: []ApplyOutcome{{
				Call:   &toolv1.ToolCall{Id: "call-i1"},
				Result: &toolv1.ToolResult{},
				Error:  &toolv1.ToolError{},
			}},
			want: ErrInvalidOutcome,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := &recordingSink{}
			g, d := decidedThreeProviderPlan(t, sink)
			result, err := g.Result(context.Background(), "turn-1", d, tt.out)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Result err = %v, want %v", err, tt.want)
			}
			if result != nil {
				t.Error("Result returned an ApplyResult alongside an error")
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			if len(sink.events) != 0 {
				t.Error("an apply event was persisted despite the error")
			}
		})
	}
}

func TestResult_appendFailurePropagates(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	g, d := decidedThreeProviderPlan(t, sink)
	sink.mu.Lock()
	sink.eventErr = errFake
	sink.mu.Unlock()

	_, err := g.Result(context.Background(), "turn-1", d, []ApplyOutcome{
		{Call: &toolv1.ToolCall{Id: "call-i1"}, Result: &toolv1.ToolResult{}},
		{Call: &toolv1.ToolCall{Id: "call-i2"}, Result: &toolv1.ToolResult{}},
	})
	if !errors.Is(err, errFake) {
		t.Fatalf("Result err = %v, want the sink's error", err)
	}
}
