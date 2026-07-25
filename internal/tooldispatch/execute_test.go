package tooldispatch

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/pluggableharness/agent/internal/circuitbreaker"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

func TestExecute_EmptyCalls(t *testing.T) {
	t.Parallel()
	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), nil)
	if err != nil || outcomes != nil {
		t.Fatalf("Execute(nil) = %v, %v; want nil, nil", outcomes, err)
	}
}

// interval is one recorded call's wall-clock execution window, tagged
// for the overlap assertions below.
type interval struct {
	label string
	start time.Time
	end   time.Time
}

func (a interval) overlaps(b interval) bool {
	return a.start.Before(b.end) && b.start.Before(a.end)
}

// recorder collects intervals from concurrently-running fake Invoke
// calls — the overlap-recorder mechanism go-testing.md's brief for this
// package calls for: a fake Invoke that timestamps entry/exit.
type recorder struct {
	mu        sync.Mutex
	intervals map[string]interval
}

func newRecorder() *recorder {
	return &recorder{intervals: make(map[string]interval)}
}

func (r *recorder) hooks(label string) (onEnter, onExit func(time.Time)) {
	onEnter = func(t time.Time) {
		r.mu.Lock()
		defer r.mu.Unlock()
		iv := r.intervals[label]
		iv.label = label
		iv.start = t
		r.intervals[label] = iv
	}
	onExit = func(t time.Time) {
		r.mu.Lock()
		defer r.mu.Unlock()
		iv := r.intervals[label]
		iv.end = t
		r.intervals[label] = iv
	}
	return onEnter, onExit
}

func (r *recorder) get(label string) interval {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.intervals[label]
}

// TestExecute_ConcurrencySpec_KeyIsolation proves (a) two calls sharing an
// identical ConcurrencySpec key never overlap, and (c) calls with
// distinct keys genuinely DO run concurrently — asserted as actual
// observed overlap, not merely "no violation observed." This
// deliberately uses ONLY safe:true operations — see
// TestExecute_ConcurrencySpec_ExclusiveExcludesProviderWide for why an
// exclusive (safe:false) call is tested separately, not mixed into this
// same scenario.
func TestExecute_ConcurrencySpec_KeyIsolation(t *testing.T) {
	t.Parallel()
	const delay = 60 * time.Millisecond
	rec := newRecorder()

	writeSpec := &toolv1.ConcurrencySpec{Safe: true, KeyFields: []string{"path"}}

	newClient := func(label string) *fakeToolClient {
		onEnter, onExit := rec.hooks(label)
		return &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
			st := resultStream(mustStruct(t, map[string]any{"ok": true}))
			st.delay = delay
			st.onEnter = onEnter
			st.onExit = onExit
			return st, nil
		}}
	}

	writeHandleA := newToolHandle("fs", "write", toolv1.ToolKind_TOOL_KIND_RESOURCE, writeSpec, nil, newClient("a1"))
	writeHandleA2 := newToolHandle("fs", "write", toolv1.ToolKind_TOOL_KIND_RESOURCE, writeSpec, nil, newClient("a2"))
	writeHandleB := newToolHandle("fs", "write", toolv1.ToolKind_TOOL_KIND_RESOURCE, writeSpec, nil, newClient("b"))

	calls := []Call{
		newCall("a1", "write", mustStruct(t, map[string]any{"path": "a.go"}), writeHandleA),
		newCall("a2", "write", mustStruct(t, map[string]any{"path": "a.go"}), writeHandleA2), // same key as a1
		newCall("b", "write", mustStruct(t, map[string]any{"path": "b.go"}), writeHandleB),   // distinct key
	}

	s, events := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), calls)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i, o := range outcomes {
		if o.Error != nil {
			t.Fatalf("outcome %d unexpectedly errored: %v", i, o.Error)
		}
	}
	if len(events.snapshot()) != 6 { // 3 tool_call + 3 tool_result
		t.Fatalf("expected 6 persisted events, got %d", len(events.snapshot()))
	}

	a1, a2, b := rec.get("a1"), rec.get("a2"), rec.get("b")

	// (a) identical key (a1, a2) must never overlap.
	if a1.overlaps(a2) {
		t.Errorf("a1 (path=a.go) and a2 (path=a.go) overlapped, but share an identical ConcurrencySpec key: %+v / %+v", a1, a2)
	}

	// (c) distinct keys (a1 or a2, vs b) must genuinely run concurrently
	// — assert actual observed overlap, not just absence of a violation.
	if !a1.overlaps(b) && !a2.overlaps(b) {
		t.Errorf("expected b (path=b.go) to overlap at least one of a1/a2 (path=a.go, distinct key) — got a1=%+v a2=%+v b=%+v", a1, a2, b)
	}
}

// TestExecute_ConcurrencySpec_ExclusiveExcludesProviderWide proves (b) no
// call overlaps a safe:false call on the same provider. Deliberately
// isolated from TestExecute_ConcurrencySpec_KeyIsolation's distinct-key
// concurrency assertion: golang.org/x/sync/semaphore.Weighted enforces
// FIFO fairness among waiters (see its notifyWaiters — a queued large
// request blocks every smaller request behind it even if capacity for
// the smaller one is technically free, specifically to avoid starving
// the large request). That means an exclusive acquire queued
// concurrently with unrelated safe:true acquires CAN legitimately delay
// (never violate — only delay) one of them, which would make a combined
// "b must overlap a1/a2, AND c must never overlap anything" assertion
// flaky depending on acquire arrival order. The safety property (b)
// tested here has no such ordering sensitivity — it holds under every
// interleaving by construction — so it is safe to assert on its own.
func TestExecute_ConcurrencySpec_ExclusiveExcludesProviderWide(t *testing.T) {
	t.Parallel()
	const delay = 40 * time.Millisecond
	rec := newRecorder()

	newClient := func(label string) *fakeToolClient {
		onEnter, onExit := rec.hooks(label)
		return &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
			st := resultStream(mustStruct(t, map[string]any{"ok": true}))
			st.delay = delay
			st.onEnter = onEnter
			st.onExit = onExit
			return st, nil
		}}
	}

	writeSpec := &toolv1.ConcurrencySpec{Safe: true, KeyFields: []string{"path"}}
	writeHandleA := newToolHandle("fs", "write", toolv1.ToolKind_TOOL_KIND_RESOURCE, writeSpec, nil, newClient("a"))
	writeHandleB := newToolHandle("fs", "write", toolv1.ToolKind_TOOL_KIND_RESOURCE, writeSpec, nil, newClient("b"))
	execSpec := &toolv1.ConcurrencySpec{Safe: false}
	execHandle := newToolHandle("fs", "exec", toolv1.ToolKind_TOOL_KIND_RESOURCE, execSpec, nil, newClient("c"))

	calls := []Call{
		newCall("a", "write", mustStruct(t, map[string]any{"path": "a.go"}), writeHandleA),
		newCall("b", "write", mustStruct(t, map[string]any{"path": "b.go"}), writeHandleB),
		newCall("c", "exec", mustStruct(t, map[string]any{}), execHandle),
	}

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), calls)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i, o := range outcomes {
		if o.Error != nil {
			t.Fatalf("outcome %d unexpectedly errored: %v", i, o.Error)
		}
	}

	a, b, c := rec.get("a"), rec.get("b"), rec.get("c")
	for label, iv := range map[string]interval{"a": a, "b": b} {
		if c.overlaps(iv) {
			t.Errorf("exec (safe:false) overlapped %s, but safe:false MUST exclude every other call on the same provider: %+v / %+v", label, c, iv)
		}
	}
}

// TestExecute_SerializeAll_NeverOverlaps proves cfg.SerializeAll collapses
// every call to strictly sequential execution even when every call
// declares a ConcurrencySpec that would otherwise permit full
// concurrency.
func TestExecute_SerializeAll_NeverOverlaps(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	spec := &toolv1.ConcurrencySpec{Safe: true} // no key_fields: fully concurrent, if honored

	labels := []string{"1", "2", "3", "4"}
	var calls []Call
	for _, label := range labels {
		onEnter, onExit := rec.hooks(label)
		client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
			st := resultStream(mustStruct(t, map[string]any{"ok": true}))
			st.delay = 20 * time.Millisecond
			st.onEnter = onEnter
			st.onExit = onExit
			return st, nil
		}}
		handle := newToolHandle("web", "search", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, spec, nil, client)
		calls = append(calls, newCall(label, "search", mustStruct(t, map[string]any{"q": label}), handle))
	}

	s, _ := testScheduler(t, Config{SerializeAll: true})
	if _, err := s.Execute(context.Background(), calls); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var ivs []interval
	for _, label := range labels {
		ivs = append(ivs, rec.get(label))
	}
	for i := range ivs {
		for j := range ivs {
			if i == j {
				continue
			}
			if ivs[i].overlaps(ivs[j]) {
				t.Fatalf("SerializeAll: %s overlapped %s: %+v / %+v", ivs[i].label, ivs[j].label, ivs[i], ivs[j])
			}
		}
	}
}

// TestExecute_OutcomesInInputOrder proves Execute returns outcomes in
// input order regardless of completion order, using randomized latencies
// deliberately inverted from input order (the last call finishes first).
func TestExecute_OutcomesInInputOrder(t *testing.T) {
	t.Parallel()
	const n = 8
	spec := &toolv1.ConcurrencySpec{Safe: true}

	var completionOrder []string
	var mu sync.Mutex

	var calls []Call
	for i := range n {
		id := string(rune('a' + i))
		delay := time.Duration(n-i) * 15 * time.Millisecond // later calls finish sooner
		client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
			st := resultStream(mustStruct(t, map[string]any{"id": id}))
			st.delay = delay
			st.onExit = func(time.Time) {
				mu.Lock()
				completionOrder = append(completionOrder, id)
				mu.Unlock()
			}
			return st, nil
		}}
		handle := newToolHandle("web", "search", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, spec, nil, client)
		calls = append(calls, newCall(id, "search", mustStruct(t, map[string]any{"q": id}), handle))
	}

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), calls)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(outcomes) != n {
		t.Fatalf("got %d outcomes, want %d", len(outcomes), n)
	}
	for i, o := range outcomes {
		wantID := string(rune('a' + i))
		if o.Call.GetId() != wantID {
			t.Errorf("outcomes[%d].Call.Id = %q, want %q (input order violated)", i, o.Call.GetId(), wantID)
		}
	}

	// Sanity: completion order actually differed from input order —
	// otherwise this test wouldn't be exercising anything interesting.
	inputOrder := true
	for i, id := range completionOrder {
		if id != string(rune('a'+i)) {
			inputOrder = false
			break
		}
	}
	if inputOrder {
		t.Skip("completion order coincidentally matched input order; scheduling jitter made this run non-discriminating")
	}
}

func TestExecute_OutputSchemaViolation(t *testing.T) {
	t.Parallel()
	outputSchema := &schemav1.Schema{
		Type:     schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Required: []string{"path"},
	}
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return resultStream(mustStruct(t, map[string]any{"wrong_field": "x"})), nil
	}}
	handle := newToolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, outputSchema, client)

	s, events := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "read_file", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	o := outcomes[0]
	if o.Result != nil {
		t.Fatalf("expected nil Result for a schema-violating payload, got %+v", o.Result)
	}
	if o.Error == nil || o.Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_UNKNOWN, got %+v", o.Error)
	}

	snap := events.snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 persisted events (tool_call, tool_result), got %d", len(snap))
	}
}

func TestExecute_OutputSchemaUnspecified_Passes(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return resultStream(mustStruct(t, map[string]any{"anything": 1})), nil
	}}
	handle := newToolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "read_file", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcomes[0].Error != nil {
		t.Fatalf("unconstrained output_schema must never fail validation, got %+v", outcomes[0].Error)
	}
}

func TestExecute_ProviderCrash_TripsBreakerAndReturnsSignal(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return nil, status.Error(codes.Unavailable, "provider process exited")
	}}
	handle := newToolHandle("flaky", "op", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	breaker := circuitbreaker.New(circuitbreaker.Config{ConsecutiveThreshold: 1})
	s, _ := testScheduler(t, Config{Breaker: breaker})

	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "op", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	o := outcomes[0]
	if o.Error == nil || o.Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_PROCESS_CRASHED {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_PROCESS_CRASHED, got %+v", o.Error)
	}
	if !o.Error.GetRetryable() {
		t.Error("process_crashed should be retryable")
	}
	tripped := o.Error.GetDetails().GetFields()["breaker_tripped"].GetBoolValue()
	if !tripped {
		t.Fatalf("expected breaker_tripped=true in Error.Details, got %+v", o.Error.GetDetails())
	}
}

func TestExecute_CrashWithoutTrip_NoSignal(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return nil, status.Error(codes.Unavailable, "provider process exited")
	}}
	handle := newToolHandle("flaky", "op", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	// Threshold 2: a single crash must not trip yet.
	breaker := circuitbreaker.New(circuitbreaker.Config{ConsecutiveThreshold: 2})
	s, _ := testScheduler(t, Config{Breaker: breaker})

	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "op", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if v := outcomes[0].Error.GetDetails().GetFields()["breaker_tripped"]; v != nil {
		t.Fatalf("breaker not yet tripped, but breaker_tripped detail present: %+v", v)
	}
}

func TestExecute_DefaultTimeout_MapsToTimeoutCategory(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		st := resultStream(mustStruct(t, map[string]any{"ok": true}))
		st.delay = 200 * time.Millisecond // far longer than the schema's default_timeout
		return st, nil
	}}
	handle := newToolHandle("slow", "op", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)
	handle.Schema.DefaultTimeout = durationOf(10 * time.Millisecond)

	s, _ := testScheduler(t, Config{DefaultTimeout: time.Hour}) // schema-level timeout MUST win over this
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "op", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	o := outcomes[0]
	if o.Error == nil || o.Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_TIMEOUT, got %+v", o.Error)
	}
}

func TestExecute_CfgDefaultTimeout_UsedWhenSchemaSilent(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		st := resultStream(mustStruct(t, map[string]any{"ok": true}))
		st.delay = 200 * time.Millisecond
		return st, nil
	}}
	handle := newToolHandle("slow", "op", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)
	// handle.Schema.DefaultTimeout left nil.

	s, _ := testScheduler(t, Config{DefaultTimeout: 10 * time.Millisecond})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "op", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcomes[0].Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_TIMEOUT, got %+v", outcomes[0].Error)
	}
}

func TestExecute_MultiEventStream(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		exitCode := int32(0)
		return &fakeInvokeStream{events: []*toolv1.ToolEvent{
			{Event: &toolv1.ToolEvent_OutputChunk_{OutputChunk: &toolv1.ToolEvent_OutputChunk{Stream: toolv1.OutputStream_OUTPUT_STREAM_STDOUT, Data: []byte("hello ")}}},
			{Event: &toolv1.ToolEvent_Progress_{Progress: &toolv1.ToolEvent_Progress{Message: "halfway"}}},
			{Event: &toolv1.ToolEvent_OutputChunk_{OutputChunk: &toolv1.ToolEvent_OutputChunk{Stream: toolv1.OutputStream_OUTPUT_STREAM_STDOUT, Data: []byte("world")}}},
			{Event: &toolv1.ToolEvent_ExitStatus_{ExitStatus: &toolv1.ToolEvent_ExitStatus{ExitCode: exitCode}}},
			{Event: &toolv1.ToolEvent_Result{Result: &toolv1.ToolResult{Payload: mustStruct(t, map[string]any{"stdout": "hello world"})}}},
		}}, nil
	}}
	handle := newToolHandle("shell", "exec", toolv1.ToolKind_TOOL_KIND_RESOURCE, &toolv1.ConcurrencySpec{Safe: false}, nil, client)

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "exec", mustStruct(t, map[string]any{"cmd": "echo"}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	o := outcomes[0]
	if o.Error != nil {
		t.Fatalf("unexpected error: %+v", o.Error)
	}
	if o.ExitCode == nil || *o.ExitCode != 0 {
		t.Fatalf("ExitCode = %v, want 0", o.ExitCode)
	}
	if got := o.Result.GetPayload().GetFields()["stdout"].GetStringValue(); got != "hello world" {
		t.Fatalf("Result payload stdout = %q, want %q", got, "hello world")
	}
}

func TestExecute_StreamClosedWithoutTerminalEvent(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return &fakeInvokeStream{events: nil}, nil // immediate EOF, no terminal event ever sent
	}}
	handle := newToolHandle("buggy", "op", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "op", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcomes[0].Error.GetCategory() != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_UNKNOWN {
		t.Fatalf("expected TOOL_ERROR_CATEGORY_UNKNOWN for a stream closed without a terminal event, got %+v", outcomes[0].Error)
	}
}

func TestExecute_ProviderEmittedError_PassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := &toolv1.ToolError{
		Category:  toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_EXECUTION_FAILED,
		Message:   "compiler error",
		Retryable: false,
	}
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return errorStream(wantErr), nil
	}}
	handle := newToolHandle("build", "compile", toolv1.ToolKind_TOOL_KIND_RESOURCE, &toolv1.ConcurrencySpec{Safe: false}, nil, client)

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "compile", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	o := outcomes[0]
	if o.Result != nil {
		t.Fatalf("expected nil Result on a provider-emitted error, got %+v", o.Result)
	}
	if o.Error.GetCategory() != wantErr.Category || o.Error.GetMessage() != wantErr.Message {
		t.Fatalf("Error = %+v, want %+v", o.Error, wantErr)
	}
}

func TestExecute_EventPersistenceFailure_ToolCall(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return resultStream(mustStruct(t, map[string]any{"ok": true})), nil
	}}
	handle := newToolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	events := &fakeEvents{failAt: 1}
	s := New(Config{Events: events, Logger: testLogger(t)})
	outcomes, err := s.Execute(context.Background(), []Call{newCall("1", "read_file", mustStruct(t, map[string]any{}), handle)})
	if err == nil {
		t.Fatal("expected an error when tool_call persistence fails")
	}
	if !errors.Is(err, errInjected) {
		t.Fatalf("error = %v, want wrapping errInjected", err)
	}
	if outcomes != nil {
		t.Fatalf("expected nil outcomes on infra failure, got %+v", outcomes)
	}
}

func TestExecute_EventPersistenceFailure_ToolResult(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return resultStream(mustStruct(t, map[string]any{"ok": true})), nil
	}}
	handle := newToolHandle("fs", "read_file", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	events := &fakeEvents{failAt: 2} // tool_call succeeds, tool_result fails
	s := New(Config{Events: events, Logger: testLogger(t)})
	_, err := s.Execute(context.Background(), []Call{newCall("1", "read_file", mustStruct(t, map[string]any{}), handle)})
	if err == nil || !errors.Is(err, errInjected) {
		t.Fatalf("error = %v, want wrapping errInjected", err)
	}
}

func TestExecute_ParentCancellation_MapsToCancelled(t *testing.T) {
	t.Parallel()
	client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		st := resultStream(mustStruct(t, map[string]any{"ok": true}))
		st.delay = time.Hour // never completes before the parent ctx is canceled
		return st, nil
	}}
	handle := newToolHandle("slow", "op", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, &toolv1.ConcurrencySpec{Safe: true}, nil, client)

	s, events := testScheduler(t, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	outcomes, err := s.Execute(ctx, []Call{newCall("1", "op", mustStruct(t, map[string]any{}), handle)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	o := outcomes[0]
	if o.Error == nil {
		t.Fatal("expected a cancellation/timeout ToolError")
	}
	cat := o.Error.GetCategory()
	if cat != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_CANCELLED && cat != toolv1.ToolErrorCategory_TOOL_ERROR_CATEGORY_TIMEOUT {
		t.Fatalf("Category = %v, want CANCELLED or TIMEOUT", cat)
	}
	// The tool_result MUST still have been persisted despite the parent
	// ctx being canceled — this is the WithoutCancel durability guarantee
	// CLAUDE.md documents.
	if len(events.snapshot()) != 2 {
		t.Fatalf("expected 2 persisted events even under cancellation, got %d", len(events.snapshot()))
	}
}

// TestExecute_Randomized_InputOrder further stresses input-order
// preservation with genuinely randomized per-call latencies (not just a
// fixed inverse schedule), run several times for confidence under -race
// -shuffle.
func TestExecute_Randomized_InputOrder(t *testing.T) {
	t.Parallel()
	const n = 12
	spec := &toolv1.ConcurrencySpec{Safe: true}

	var calls []Call
	for i := range n {
		id := string(rune('a' + i))
		delay := time.Duration(rand.IntN(30)) * time.Millisecond
		client := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
			st := resultStream(mustStruct(t, map[string]any{"id": id}))
			st.delay = delay
			return st, nil
		}}
		handle := newToolHandle("web", "search", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE, spec, nil, client)
		calls = append(calls, newCall(id, "search", mustStruct(t, map[string]any{"q": id}), handle))
	}

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), calls)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i, o := range outcomes {
		wantID := string(rune('a' + i))
		if o.Call.GetId() != wantID {
			t.Fatalf("outcomes[%d].Call.Id = %q, want %q", i, o.Call.GetId(), wantID)
		}
	}
}

func durationOf(d time.Duration) *durationpb.Duration {
	return durationpb.New(d)
}

// TestExecute_QueueTimeIsNotChargedToTheCallTimeout pins the boundary
// ToolSchema.default_timeout actually measures.
//
// The deadline used to be derived before acquireLocks, so its clock ran
// while a call was still queued behind an exclusive (safe:false) sibling
// holding the provider-wide semaphore. A short-timeout call that never got
// to run came back as TOOL_ERROR_CATEGORY_TIMEOUT — reporting a provider
// failure for an operation that was never invoked, and marking it
// Retryable so a caller would try it again. The timeout is documented as
// the Invoke deadline, so it now starts only once the locks are held.
//
// The blocker's own delay is comfortably longer than the queued call's
// timeout: if queue time were charged, this test would fail deterministically.
func TestExecute_QueueTimeIsNotChargedToTheCallTimeout(t *testing.T) {
	t.Parallel()
	const (
		blockerDelay = 120 * time.Millisecond
		queuedBudget = 30 * time.Millisecond
	)

	slowClient := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		st := resultStream(mustStruct(t, map[string]any{"ok": true}))
		st.delay = blockerDelay
		return st, nil
	}}
	fastClient := &fakeToolClient{invokeFunc: func(int, context.Context, *toolv1.ToolCall) (*fakeInvokeStream, error) {
		return resultStream(mustStruct(t, map[string]any{"ok": true})), nil
	}}

	// The blocker is safe:false, so it takes the provider semaphore
	// exclusively and every sibling waits for it.
	blocker := newToolHandle("fs", "exec", toolv1.ToolKind_TOOL_KIND_RESOURCE,
		&toolv1.ConcurrencySpec{Safe: false}, nil, slowClient)

	// The queued call declares a deadline far shorter than the blocker's
	// runtime, but its own execution is effectively instant.
	queued := newToolHandle("fs", "read", toolv1.ToolKind_TOOL_KIND_DATA_SOURCE,
		&toolv1.ConcurrencySpec{Safe: true}, nil, fastClient)
	queued.Schema.DefaultTimeout = durationpb.New(queuedBudget)

	calls := []Call{
		newCall("blocker", "exec", mustStruct(t, map[string]any{}), blocker),
		newCall("queued", "read", mustStruct(t, map[string]any{}), queued),
	}

	s, _ := testScheduler(t, Config{})
	outcomes, err := s.Execute(context.Background(), calls)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for i, o := range outcomes {
		if o.Error != nil {
			t.Fatalf("outcome %d (%s) errored with %v: a call that waited on a lock must not be charged its Invoke deadline for the wait",
				i, o.Call.GetToolName(), o.Error.GetCategory())
		}
		if o.Result == nil {
			t.Fatalf("outcome %d (%s) has neither result nor error", i, o.Call.GetToolName())
		}
	}
}
