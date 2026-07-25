package tooldispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/interactive"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func TestExecuteInteractive_EmptyCalls(t *testing.T) {
	t.Parallel()
	s, _ := testScheduler(t, Config{Interactive: &fakeResolver{}})
	outcomes, err := s.ExecuteInteractive(context.Background(), nil)
	if err != nil || outcomes != nil {
		t.Fatalf("ExecuteInteractive(nil) = %v, %v; want nil, nil", outcomes, err)
	}
}

// TestExecuteInteractive_Sequential proves interactive calls never
// overlap each other — the strictly-sequential requirement
// tool/protocol.md#kind-interactive imposes regardless of any declared
// ConcurrencySpec.
func TestExecuteInteractive_Sequential(t *testing.T) {
	t.Parallel()
	const n = 5
	var (
		mu       sync.Mutex
		inFlight int
		maxSeen  int
	)

	resolver := &fakeResolver{
		onCall: func(int) {
			mu.Lock()
			inFlight++
			if inFlight > maxSeen {
				maxSeen = inFlight
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			inFlight--
			mu.Unlock()
		},
	}
	for range n {
		resolver.responses = append(resolver.responses, interactive.Response{Payload: mustStruct(t, map[string]any{"answer": "ok"})})
		resolver.errs = append(resolver.errs, nil)
	}

	var calls []Call
	for i := range n {
		handle := newToolHandle("frontend", "ask_user", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, nil, nil, nil)
		calls = append(calls, newCall(string(rune('a'+i)), "ask_user", mustStruct(t, map[string]any{}), handle))
	}

	s, _ := testScheduler(t, Config{Interactive: resolver})
	outcomes, err := s.ExecuteInteractive(context.Background(), calls)
	if err != nil {
		t.Fatalf("ExecuteInteractive: %v", err)
	}
	if len(outcomes) != n {
		t.Fatalf("got %d outcomes, want %d", len(outcomes), n)
	}
	for i, o := range outcomes {
		if o.Error != nil {
			t.Fatalf("outcome %d unexpectedly errored: %v", i, o.Error)
		}
	}
	if maxSeen > 1 {
		t.Fatalf("interactive calls overlapped: max concurrent in-flight = %d, want 1", maxSeen)
	}
}

func TestExecuteInteractive_NoFrontend_MapsToPermissionDenied(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{
		responses: []interactive.Response{{}},
		errs:      []error{interactive.ErrNoFrontend},
	}
	handle := newToolHandle("frontend", "ask_user", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, nil, nil, nil)

	s, _ := testScheduler(t, Config{Interactive: resolver})
	outcomes, err := s.ExecuteInteractive(context.Background(), []Call{
		newCall("1", "ask_user", mustStruct(t, map[string]any{}), handle),
	})
	if err != nil {
		t.Fatalf("ExecuteInteractive: %v", err)
	}
	o := outcomes[0]
	if o.Error == nil || o.Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PERMISSION_DENIED {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_PERMISSION_DENIED, got %+v", o.Error)
	}
}

func TestExecuteInteractive_OutputSchemaViolation(t *testing.T) {
	t.Parallel()
	outputSchema := &schemav1.Schema{
		Type:     schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Required: []string{"answer"},
	}
	resolver := &fakeResolver{
		responses: []interactive.Response{{Payload: mustStruct(t, map[string]any{"wrong_field": "x"})}},
		errs:      []error{nil},
	}
	handle := newToolHandle("frontend", "ask_user", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, nil, outputSchema, nil)

	s, events := testScheduler(t, Config{Interactive: resolver})
	outcomes, err := s.ExecuteInteractive(context.Background(), []Call{
		newCall("1", "ask_user", mustStruct(t, map[string]any{}), handle),
	})
	if err != nil {
		t.Fatalf("ExecuteInteractive: %v", err)
	}
	o := outcomes[0]
	if o.Result != nil {
		t.Fatalf("expected nil Result for a schema-violating answer, got %+v", o.Result)
	}
	if o.Error == nil || o.Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_UNKNOWN, got %+v", o.Error)
	}
	if len(events.snapshot()) != 2 {
		t.Fatalf("expected 2 persisted events, got %d", len(events.snapshot()))
	}
}

func TestExecuteInteractive_SuccessfulAnswer(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{
		responses: []interactive.Response{{Payload: mustStruct(t, map[string]any{"answer": "yes"})}},
		errs:      []error{nil},
	}
	handle := newToolHandle("frontend", "ask_user", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, nil, nil, nil)

	s, _ := testScheduler(t, Config{Interactive: resolver})
	outcomes, err := s.ExecuteInteractive(context.Background(), []Call{
		newCall("1", "ask_user", mustStruct(t, map[string]any{}), handle),
	})
	if err != nil {
		t.Fatalf("ExecuteInteractive: %v", err)
	}
	o := outcomes[0]
	if o.Error != nil {
		t.Fatalf("unexpected error: %+v", o.Error)
	}
	if got := o.Result.GetPayload().GetFields()["answer"].GetStringValue(); got != "yes" {
		t.Fatalf("Result payload answer = %q, want %q", got, "yes")
	}
	if len(resolver.calls) != 1 || resolver.calls[0].CallID != "1" || resolver.calls[0].ToolName != "ask_user" {
		t.Fatalf("Resolve called with unexpected Request: %+v", resolver.calls)
	}
}

func TestExecuteInteractive_Cancellation(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{
		onCall: func(int) {},
	}
	resolver.responses = []interactive.Response{{}}
	resolver.errs = []error{context.Canceled}
	handle := newToolHandle("frontend", "ask_user", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, nil, nil, nil)

	s, events := testScheduler(t, Config{Interactive: resolver})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcomes, err := s.ExecuteInteractive(ctx, []Call{
		newCall("1", "ask_user", mustStruct(t, map[string]any{}), handle),
	})
	if err != nil {
		t.Fatalf("ExecuteInteractive: %v", err)
	}
	o := outcomes[0]
	if o.Error == nil || o.Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_CANCELLED, got %+v", o.Error)
	}
	// Persistence MUST still have happened despite the canceled ctx.
	if len(events.snapshot()) != 2 {
		t.Fatalf("expected 2 persisted events even under cancellation, got %d", len(events.snapshot()))
	}
}

func TestExecuteInteractive_EventPersistenceFailure(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{
		responses: []interactive.Response{{Payload: mustStruct(t, map[string]any{})}},
		errs:      []error{nil},
	}
	handle := newToolHandle("frontend", "ask_user", toolv1.ToolKind_TOOL_KIND_INTERACTIVE, nil, nil, nil)

	events := &fakeEvents{failAt: 1}
	s := New(Config{Events: events, Logger: testLogger(t), Interactive: resolver})
	outcomes, err := s.ExecuteInteractive(context.Background(), []Call{
		newCall("1", "ask_user", mustStruct(t, map[string]any{}), handle),
	})
	if err == nil {
		t.Fatal("expected an error when tool_call persistence fails")
	}
	if outcomes != nil {
		t.Fatalf("expected nil outcomes on infra failure, got %+v", outcomes)
	}
}
