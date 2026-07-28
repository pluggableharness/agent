package pending_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/pending"
	"github.com/pluggableharness/agent/internal/plandecision"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

func TestPlanBridge_ResolveAnswer(t *testing.T) {
	t.Parallel()

	b := pending.NewPlanBridge()
	req := plandecision.Request{
		SessionID: "s1",
		TurnID:    "t1",
		Item:      &planv1.PlanItem{Id: "item-1"},
	}

	done := make(chan plandecision.Decision, 1)
	errCh := make(chan error, 1)
	go func() {
		dec, err := b.Resolve(context.Background(), req)
		errCh <- err
		done <- dec
	}()

	// Wait until waiter is registered.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := b.Answer("s1", "item-1", plandecision.Decision{
			Decision:  planv1.PlanDecision_PLAN_DECISION_ALLOW,
			Scope:     planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE,
			DecidedBy: "operator",
		})
		if err == nil {
			break
		}
		if !errors.Is(err, pending.ErrNoWaiter) {
			t.Fatalf("Answer: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	dec := <-done
	if dec.Decision != planv1.PlanDecision_PLAN_DECISION_ALLOW {
		t.Errorf("decision = %v", dec.Decision)
	}
}

func TestPlanBridge_Cancel(t *testing.T) {
	t.Parallel()

	b := pending.NewPlanBridge()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := b.Resolve(ctx, plandecision.Request{
			SessionID: "s",
			Item:      &planv1.PlanItem{Id: "i"},
		})
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want cancel error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}
