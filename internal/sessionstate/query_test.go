package sessionstate

import (
	"context"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/statebackend"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestLive_Meta(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	meta, err := live.Meta(context.Background())
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if meta.SessionID != live.id {
		t.Errorf("Meta().SessionID = %q, want %q", meta.SessionID, live.id)
	}
	if meta.Profile != "default" {
		t.Errorf("Meta().Profile = %q, want %q", meta.Profile, "default")
	}
}

func TestLive_Meta_afterCloseFails(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := live.Meta(context.Background()); err == nil {
		t.Fatal("Meta after Close = nil error, want an error")
	}
}

func TestLive_TotalCostUSD(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{MaxCostUSD: 100}, nil, time.Time{})

	total, err := live.TotalCostUSD(context.Background())
	if err != nil {
		t.Fatalf("TotalCostUSD: %v", err)
	}
	if total != 0 {
		t.Errorf("TotalCostUSD() before any spend = %v, want 0", total)
	}

	cost := statebackend.CostEntry{ProviderName: "anthropic", ModelID: "claude", CostUSD: 3.5}
	ev := kernelEvent(t, testProducer(), kernelv1.EventKind_EVENT_KIND_MESSAGE, []byte("x"))
	if _, err := live.AppendMessage(context.Background(), ev, cost); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	total, err = live.TotalCostUSD(context.Background())
	if err != nil {
		t.Fatalf("TotalCostUSD after spend: %v", err)
	}
	if total != cost.CostUSD {
		t.Errorf("TotalCostUSD() after spend = %v, want %v", total, cost.CostUSD)
	}
}

func TestLive_Events(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	rec := EmitRecord{
		Producer:      testProducer(),
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("x"),
	}
	if _, err := live.Emit(context.Background(), rec); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := live.Emit(context.Background(), rec); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	var got []statebackend.Event
	for ev, evErr := range live.Events(context.Background(), statebackend.EventQuery{}) {
		if evErr != nil {
			t.Fatalf("Events: %v", evErr)
		}
		got = append(got, ev)
	}
	if len(got) != 2 {
		t.Fatalf("Events count = %d, want 2", len(got))
	}
	if got[0].Sequence != 1 || got[1].Sequence != 2 {
		t.Errorf("Events sequences = [%d %d], want [1 2]", got[0].Sequence, got[1].Sequence)
	}
}

func TestLive_Events_filteredByKind(t *testing.T) {
	t.Parallel()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	if _, err := live.Emit(context.Background(), EmitRecord{
		Producer: testProducer(), Kind: kernelv1.EventKind_EVENT_KIND_TOOL_CALL, SchemaVersion: "1", Payload: []byte("a"),
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := live.Emit(context.Background(), EmitRecord{
		Producer: testProducer(), Kind: kernelv1.EventKind_EVENT_KIND_TOOL_RESULT, SchemaVersion: "1", Payload: []byte("b"),
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	var got []statebackend.Event
	q := statebackend.EventQuery{Kinds: []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_TOOL_RESULT}}
	for ev, evErr := range live.Events(context.Background(), q) {
		if evErr != nil {
			t.Fatalf("Events: %v", evErr)
		}
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("filtered Events count = %d, want 1", len(got))
	}
	if got[0].Kind != kernelv1.EventKind_EVENT_KIND_TOOL_RESULT {
		t.Errorf("filtered Events[0].Kind = %v, want EVENT_KIND_TOOL_RESULT", got[0].Kind)
	}
}
