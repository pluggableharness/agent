package sessionstate

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/statebackend"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

// subscribeCollect subscribes to topic on bus and returns a channel that
// receives every republished *kernelv1.BusEvent, cleaned up via
// t.Cleanup.
func subscribeCollect(t *testing.T, bus *eventbus.Bus, topic string) <-chan *kernelv1.BusEvent {
	t.Helper()
	got := make(chan *kernelv1.BusEvent, 8)
	sub, err := bus.Subscribe(context.Background(), topic, func(_ context.Context, ev eventbus.Event) {
		busEvent, ok := ev.Payload.(*kernelv1.BusEvent)
		if !ok {
			t.Errorf("subscribeCollect: payload is %T, want *kernelv1.BusEvent", ev.Payload)
			return
		}
		got <- busEvent
	})
	if err != nil {
		t.Fatalf("Subscribe(%q): %v", topic, err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	return got
}

// waitForBusEvent waits up to a short bound for one event on ch, failing
// the test on timeout.
func waitForBusEvent(t *testing.T, ch <-chan *kernelv1.BusEvent) *kernelv1.BusEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for republished event")
		return nil
	}
}

func TestLive_Emit_writesAndRepublishes(t *testing.T) {
	t.Parallel()
	now := time.Now()
	live, bus := newTestLive(t, bounds.Limits{}, nil, now)
	got := subscribeCollect(t, bus, "kernel.event.tool_call")

	producer := testProducer()
	rec := EmitRecord{
		Producer:      producer,
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("payload-bytes"),
	}

	outcome, err := live.Emit(context.Background(), rec)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if outcome.ID == "" {
		t.Fatal("Emit outcome ID is empty")
	}
	if outcome.Sequence != 1 {
		t.Errorf("Emit outcome Sequence = %d, want 1", outcome.Sequence)
	}

	busEvent := waitForBusEvent(t, got)
	if busEvent.GetTopic() != "kernel.event.tool_call" {
		t.Errorf("BusEvent.Topic = %q, want %q", busEvent.GetTopic(), "kernel.event.tool_call")
	}
	if !bytes.Equal(busEvent.GetPayload(), rec.Payload) {
		t.Errorf("BusEvent.Payload = %q, want %q", busEvent.GetPayload(), rec.Payload)
	}
	if busEvent.GetPayloadType() != "pluggableharness.event.v1.ToolCallEvent" {
		t.Errorf("BusEvent.PayloadType = %q, want %q", busEvent.GetPayloadType(), "pluggableharness.event.v1.ToolCallEvent")
	}
	if busEvent.GetSchemaVersion() != "1" {
		t.Errorf("BusEvent.SchemaVersion = %q, want %q", busEvent.GetSchemaVersion(), "1")
	}
	if !busEvent.GetTime().AsTime().Equal(now) {
		t.Errorf("BusEvent.Time = %v, want %v", busEvent.GetTime().AsTime(), now)
	}

	// The persisted row must match what was published.
	var found *statebackend.Event
	for ev, evErr := range live.session.Events(context.Background()) {
		if evErr != nil {
			t.Fatalf("Events: %v", evErr)
		}
		e := ev
		found = &e
	}
	if found == nil {
		t.Fatal("no persisted event found")
	}
	if found.ID != outcome.ID {
		t.Errorf("persisted ID = %q, want %q", found.ID, outcome.ID)
	}
	if found.Sequence != outcome.Sequence {
		t.Errorf("persisted Sequence = %d, want %d", found.Sequence, outcome.Sequence)
	}
	if !bytes.Equal(found.Payload, rec.Payload) {
		t.Errorf("persisted Payload = %q, want %q", found.Payload, rec.Payload)
	}
	if found.Kind != rec.Kind {
		t.Errorf("persisted Kind = %v, want %v", found.Kind, rec.Kind)
	}
}

func TestLive_Emit_republishFailureStillSucceedsDurably(t *testing.T) {
	t.Parallel()
	live, bus := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	// Close the bus so Publish returns eventbus.ErrClosed — Emit must
	// still succeed since the sqlite write already committed.
	if err := bus.Close(); err != nil {
		t.Fatalf("bus.Close: %v", err)
	}

	rec := EmitRecord{
		Producer:      testProducer(),
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_RESULT,
		SchemaVersion: "1",
		Payload:       []byte("result"),
	}

	outcome, err := live.Emit(context.Background(), rec)
	if err != nil {
		t.Fatalf("Emit with closed bus: %v", err)
	}

	var count int
	for _, evErr := range live.session.Events(context.Background()) {
		if evErr != nil {
			t.Fatalf("Events: %v", evErr)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("persisted event count = %d, want 1 (durable write must survive a republish failure)", count)
	}
	if outcome.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", outcome.Sequence)
	}
}

// kernelEvent builds the already-identified statebackend.Event a
// kernel-side collaborator hands to Live's Append* methods. Unlike an
// EmitRecord, the caller owns the id and the timestamp — that ownership is
// the whole reason those methods take an Event rather than a record.
func kernelEvent(t *testing.T, producer *commonv1.ProducerRef, kind kernelv1.EventKind, payload []byte) statebackend.Event {
	t.Helper()
	now := time.Unix(1700000000, 0).UTC()
	return statebackend.Event{
		ID:            statebackend.NewEventID(now),
		Timestamp:     now,
		Kind:          kind,
		Producer:      producer,
		SchemaVersion: "1",
		Payload:       payload,
	}
}

func TestLive_AppendMessage_writesCostLedgerAndRepublishes(t *testing.T) {
	t.Parallel()
	live, bus := newTestLive(t, bounds.Limits{MaxCostUSD: 100}, nil, time.Time{})
	got := subscribeCollect(t, bus, "kernel.event.message")

	ev := kernelEvent(t, testProducer(), kernelv1.EventKind_EVENT_KIND_MESSAGE, []byte("message-payload"))
	cost := statebackend.CostEntry{
		ProviderName: "anthropic",
		ModelID:      "claude",
		InputTokens:  10,
		OutputTokens: 20,
		CostUSD:      1.5,
	}

	seq, err := live.AppendMessage(context.Background(), ev, cost)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if seq != 1 {
		t.Errorf("sequence = %d, want 1", seq)
	}

	busEvent := waitForBusEvent(t, got)
	if busEvent.GetTopic() != "kernel.event.message" {
		t.Errorf("BusEvent.Topic = %q, want %q", busEvent.GetTopic(), "kernel.event.message")
	}

	entries, err := live.session.CostLedger(context.Background())
	if err != nil {
		t.Fatalf("CostLedger: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("CostLedger entries = %d, want 1", len(entries))
	}
	if entries[0].CostUSD != cost.CostUSD {
		t.Errorf("CostLedger[0].CostUSD = %v, want %v", entries[0].CostUSD, cost.CostUSD)
	}
	if entries[0].ModelID != cost.ModelID {
		t.Errorf("CostLedger[0].ModelID = %q, want %q", entries[0].ModelID, cost.ModelID)
	}
}

// TestLive_AppendMessage_doesNotDebitBudget pins the single-debit rule.
// internal/session's absorb debits turn.Result.CostUSD exactly once per
// turn; debiting here as well would count every completion twice, and the
// two would compound silently rather than fail. The parent tracker is
// asserted alongside because bounds.Tracker.Debit walks the ancestor
// chain, so a stray debit here would corrupt every ancestor too.
func TestLive_AppendMessage_doesNotDebitBudget(t *testing.T) {
	t.Parallel()
	parent := bounds.NewTracker(bounds.Limits{MaxCostUSD: 100}, nil)
	live, _ := newTestLive(t, bounds.Limits{MaxCostUSD: 100}, parent, time.Time{})

	ev := kernelEvent(t, testProducer(), kernelv1.EventKind_EVENT_KIND_MESSAGE, []byte("x"))
	if _, err := live.AppendMessage(context.Background(), ev, statebackend.CostEntry{CostUSD: 2.25}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	if got := live.Budget().TotalCostUSD(); got != 0 {
		t.Errorf("Budget().TotalCostUSD() = %v, want 0 (the session driver owns the debit, not this package)", got)
	}
	if got := parent.TotalCostUSD(); got != 0 {
		t.Errorf("parent.TotalCostUSD() = %v, want 0 (no debit here means no ancestor rollup here)", got)
	}
}

func TestLive_AppendPlan_writesPlanItemsWithKernelProducer(t *testing.T) {
	t.Parallel()
	live, bus := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	got := subscribeCollect(t, bus, "kernel.event.plan")

	ev := kernelEvent(t, statebackend.KernelProducer(), kernelv1.EventKind_EVENT_KIND_PLAN, []byte("plan-payload"))
	items := []statebackend.PlanItem{
		{
			TurnID:       "turn-1",
			ToolCallID:   "call-1",
			ProviderName: "ripgrep",
			ToolName:     "search",
			Decision:     planv1.PlanDecision_PLAN_DECISION_ALLOW,
			DecidedBy:    "policy",
		},
		{
			TurnID:       "turn-1",
			ToolCallID:   "call-2",
			ProviderName: "ripgrep",
			ToolName:     "write",
			Decision:     planv1.PlanDecision_PLAN_DECISION_ASK,
			DecidedBy:    "policy",
		},
	}

	if _, err := live.AppendPlan(context.Background(), ev, items); err != nil {
		t.Fatalf("AppendPlan: %v", err)
	}

	busEvent := waitForBusEvent(t, got)
	if busEvent.GetTopic() != "kernel.event.plan" {
		t.Errorf("BusEvent.Topic = %q, want %q", busEvent.GetTopic(), "kernel.event.plan")
	}

	planItems, err := live.session.PlanItems(context.Background())
	if err != nil {
		t.Fatalf("PlanItems: %v", err)
	}
	if len(planItems) != len(items) {
		t.Fatalf("PlanItems count = %d, want %d", len(planItems), len(items))
	}
	for i, item := range planItems {
		if item.ToolCallID != items[i].ToolCallID {
			t.Errorf("PlanItems[%d].ToolCallID = %q, want %q", i, item.ToolCallID, items[i].ToolCallID)
		}
		if item.Decision != items[i].Decision {
			t.Errorf("PlanItems[%d].Decision = %v, want %v", i, item.Decision, items[i].Decision)
		}
	}
}

// TestLive_Emit_concurrentSequencesAreExactlyOneToN mirrors
// internal/statebackend's own TestSession_AppendEvent_concurrentSequencesAreExactlyOneToN
// pattern: N concurrent Emit calls on one Live must come back with
// sequence numbers that are exactly 1..N, no duplicates or gaps, since
// Live.mu serializes every call.
func TestLive_Emit_concurrentSequencesAreExactlyOneToN(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	const n = 50
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seqs []int64
	)
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			rec := EmitRecord{
				Producer:      testProducer(),
				Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
				SchemaVersion: "1",
				Payload:       []byte{byte(idx)},
			}
			outcome, err := live.Emit(context.Background(), rec)
			if err != nil {
				t.Errorf("Emit[%d]: %v", idx, err)
				return
			}
			mu.Lock()
			seqs = append(seqs, outcome.Sequence)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(seqs) != n {
		t.Fatalf("got %d outcomes, want %d", len(seqs), n)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		want := int64(i + 1)
		if seq != want {
			t.Fatalf("sequences = %v, want exactly 1..%d with no gaps/dupes (sorted index %d = %d, want %d)", seqs, n, i, seq, want)
		}
	}
}

func TestLive_AppendMessage_afterCloseFails(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ev := kernelEvent(t, testProducer(), kernelv1.EventKind_EVENT_KIND_MESSAGE, []byte("x"))
	_, err := live.AppendMessage(context.Background(), ev, statebackend.CostEntry{CostUSD: 1})
	if !errors.Is(err, statebackend.ErrClosed) {
		t.Errorf("AppendMessage after Close error = %v, want wrapping statebackend.ErrClosed", err)
	}
}

func TestLive_AppendEvent_afterCloseFails(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ev := kernelEvent(t, testProducer(), kernelv1.EventKind_EVENT_KIND_TOOL_CALL, []byte("x"))
	if _, err := live.AppendEvent(context.Background(), ev); !errors.Is(err, statebackend.ErrClosed) {
		t.Errorf("AppendEvent after Close error = %v, want wrapping statebackend.ErrClosed", err)
	}
}

func TestLive_AppendPlan_afterCloseFails(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ev := kernelEvent(t, statebackend.KernelProducer(), kernelv1.EventKind_EVENT_KIND_PLAN, []byte("x"))
	if _, err := live.AppendPlan(context.Background(), ev, nil); !errors.Is(err, statebackend.ErrClosed) {
		t.Errorf("AppendPlan after Close error = %v, want wrapping statebackend.ErrClosed", err)
	}
}

// TestLive_AppendEvent_republishesEveryKernelKind is the regression test
// for the defect these methods exist to fix: kernel-originated events used
// to be written straight to the wrapped *statebackend.Session, so they
// persisted correctly and reached the bus never. A subscriber to
// kernel.event.* saw only other plugins' Emit calls.
func TestLive_AppendEvent_republishesEveryKernelKind(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind  kernelv1.EventKind
		topic string
	}{
		{kernelv1.EventKind_EVENT_KIND_TOOL_CALL, "kernel.event.tool_call"},
		{kernelv1.EventKind_EVENT_KIND_TOOL_RESULT, "kernel.event.tool_result"},
		{kernelv1.EventKind_EVENT_KIND_CONTEXT_CONTRIBUTION, "kernel.event.context_contribution"},
		{kernelv1.EventKind_EVENT_KIND_HOOK_ERROR, "kernel.event.hook_error"},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			t.Parallel()
			live, bus := newTestLive(t, bounds.Limits{}, nil, time.Time{})
			got := subscribeCollect(t, bus, tc.topic)

			ev := kernelEvent(t, testProducer(), tc.kind, []byte("payload"))
			if _, err := live.AppendEvent(context.Background(), ev); err != nil {
				t.Fatalf("AppendEvent: %v", err)
			}

			busEvent := waitForBusEvent(t, got)
			if busEvent.GetTopic() != tc.topic {
				t.Errorf("BusEvent.Topic = %q, want %q", busEvent.GetTopic(), tc.topic)
			}
		})
	}
}

func TestLive_Emit_invalidKindFails(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	_, err := live.Emit(context.Background(), EmitRecord{
		Producer:      testProducer(),
		Kind:          kernelv1.EventKind_EVENT_KIND_UNSPECIFIED,
		SchemaVersion: "1",
		Payload:       []byte("x"),
	})
	if err == nil {
		t.Fatal("Emit with EVENT_KIND_UNSPECIFIED = nil error, want error")
	}
	if !errors.Is(err, statebackend.ErrInvalidKind) {
		t.Errorf("Emit error = %v, want wrapping statebackend.ErrInvalidKind", err)
	}
}
