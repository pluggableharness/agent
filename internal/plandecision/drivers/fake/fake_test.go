package fake_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/fake"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

func request(id string) plandecision.Request {
	return plandecision.Request{SessionID: "sess-1", Item: &planv1.PlanItem{Id: id}}
}

func TestResolver_scriptedQueue(t *testing.T) {
	t.Parallel()

	corrected, err := structpb.NewStruct(map[string]any{"path": "/tmp/safe"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	scriptErr := errors.New("frontend detached")

	responses := []fake.Response{
		{Decision: plandecision.Decision{
			Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
			Scope:     frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
			DecidedBy: "test",
		}},
		{Decision: plandecision.Decision{
			Decision:  planv1.PlanDecision_PLAN_DECISION_DENY,
			Scope:     frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION,
			DecidedBy: "test",
		}},
		{Decision: plandecision.Decision{
			Decision:       planv1.PlanDecision_PLAN_DECISION_ALLOW,
			Scope:          frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS,
			CorrectedInput: corrected,
			DecidedBy:      "test",
		}},
		{Err: scriptErr},
	}

	r := fake.New(responses...)

	for i, want := range responses {
		got, err := r.Resolve(context.Background(), request("pi-"+string(rune('a'+i))))
		if !errors.Is(err, want.Err) {
			t.Fatalf("Resolve #%d: error = %v, want %v", i, err, want.Err)
		}
		if got.Decision != want.Decision.Decision || got.Scope != want.Decision.Scope {
			t.Errorf("Resolve #%d: got %v/%v, want %v/%v", i, got.Decision, got.Scope, want.Decision.Decision, want.Decision.Scope)
		}
	}

	if _, err := r.Resolve(context.Background(), request("pi-overflow")); !errors.Is(err, fake.ErrExhausted) {
		t.Fatalf("Resolve past the script: error = %v, want errors.Is ErrExhausted", err)
	}

	calls := r.Calls()
	if len(calls) != len(responses)+1 {
		t.Fatalf("Calls() = %d, want %d", len(calls), len(responses)+1)
	}
	if calls[0].Item.GetId() != "pi-a" {
		t.Errorf("Calls()[0].Item.Id = %q, want pi-a", calls[0].Item.GetId())
	}

	r.Reset()
	if got := len(r.Calls()); got != 0 {
		t.Fatalf("Calls() after Reset = %d, want 0", got)
	}
	if _, err := r.Resolve(context.Background(), request("pi-a")); err != nil {
		t.Fatalf("Resolve after Reset: %v", err)
	}
}

func TestResolver_always(t *testing.T) {
	t.Parallel()

	r := fake.NewAlways(fake.Response{Decision: plandecision.Decision{
		Decision:  planv1.PlanDecision_PLAN_DECISION_DENY,
		Scope:     frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
		DecidedBy: "test",
	}})

	for i := range 4 {
		got, err := r.Resolve(context.Background(), request("pi-1"))
		if err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
		if got.Decision != planv1.PlanDecision_PLAN_DECISION_DENY {
			t.Fatalf("Resolve #%d: Decision = %v, want PLAN_DECISION_DENY", i, got.Decision)
		}
	}
	if got := len(r.Calls()); got != 4 {
		t.Fatalf("Calls() = %d, want 4", got)
	}
}

func TestResolver_zeroValueIsExhausted(t *testing.T) {
	t.Parallel()

	var r fake.Resolver
	if _, err := r.Resolve(context.Background(), request("pi-1")); !errors.Is(err, fake.ErrExhausted) {
		t.Fatalf("Resolve: error = %v, want errors.Is ErrExhausted", err)
	}
}

func TestResolver_honorsContextCancellation(t *testing.T) {
	t.Parallel()

	r := fake.NewAlways(fake.Response{Decision: plandecision.Decision{
		Decision: planv1.PlanDecision_PLAN_DECISION_ALLOW,
	}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := r.Resolve(ctx, request("pi-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve: error = %v, want errors.Is context.Canceled", err)
	}
	if got := len(r.Calls()); got != 0 {
		t.Fatalf("Calls() = %d, want 0 — a cancelled Resolve must not record a call", got)
	}
}
