package tooldispatch

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// fakeToolClient is a hand-written toolv1.ToolServiceClient fake
// (go-testing.md: fakes, not mocking frameworks). Only Invoke is
// meaningful; every other method panics if called, since this package
// never calls them. The embedded nil ToolServiceClient supplies those
// panicking methods for free — same convention as
// internal/tokencount/helpers_test.go's fakeModelClient and
// internal/providercatalog/drivers/fake/fake_test.go's stubToolClient.
type fakeToolClient struct {
	toolv1.ToolServiceClient

	mu         sync.Mutex
	calls      int
	invokeFunc func(callNum int, ctx context.Context, call *toolv1.ToolCall) (*fakeInvokeStream, error)
}

func (f *fakeToolClient) Invoke(ctx context.Context, in *toolv1.InvokeRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[toolv1.InvokeResponse], error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()

	stream, err := f.invokeFunc(n, ctx, in.GetCall())
	if err != nil {
		return nil, err
	}
	stream.ctx = ctx
	return stream, nil
}

// fakeInvokeStream is a scripted grpc.ServerStreamingClient[InvokeResponse]:
// it optionally sleeps once (simulating the provider doing work, so tests
// can force overlap windows), then replays events in order, then either
// returns io.EOF (a well-behaved provider already emitted its terminal
// event) or a scripted trailing error (a stream that fails outright).
type fakeInvokeStream struct {
	ctx context.Context

	delay  time.Duration
	events []*toolv1.ToolEvent
	idx    int
	// trailingErr, if set, is returned once events is exhausted instead
	// of io.EOF.
	trailingErr error

	// onEnter/onExit, if set, are called once at stream start/terminal
	// event, with wall-clock timestamps — the overlap recorder's hook.
	onEnter func(t time.Time)
	onExit  func(t time.Time)
	entered bool
	exited  bool
}

func (s *fakeInvokeStream) Recv() (*toolv1.InvokeResponse, error) {
	if !s.entered {
		s.entered = true
		if s.onEnter != nil {
			s.onEnter(time.Now())
		}
	}

	if s.delay > 0 {
		d := s.delay
		s.delay = 0 // only sleep once, on first Recv
		select {
		case <-time.After(d):
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}

	if s.idx >= len(s.events) {
		s.markExit()
		if s.trailingErr != nil {
			return nil, s.trailingErr
		}
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	if s.idx >= len(s.events) && s.trailingErr == nil {
		// last scripted event; if it's terminal, mark exit now so an
		// overlap recorder sees the true end of "productive work,"
		// matching a real provider closing its stream promptly after
		// its terminal event.
		s.markExit()
	}
	return &toolv1.InvokeResponse{Event: ev}, nil
}

func (s *fakeInvokeStream) markExit() {
	if !s.exited {
		s.exited = true
		if s.onExit != nil {
			s.onExit(time.Now())
		}
	}
}

func (s *fakeInvokeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeInvokeStream) Trailer() metadata.MD         { return nil }
func (s *fakeInvokeStream) CloseSend() error             { return nil }
func (s *fakeInvokeStream) Context() context.Context     { return s.ctx }
func (s *fakeInvokeStream) SendMsg(any) error            { return nil }
func (s *fakeInvokeStream) RecvMsg(any) error            { return nil }

// resultStream builds a fakeInvokeStream whose only event is a terminal
// result carrying payload.
func resultStream(payload *structpb.Struct) *fakeInvokeStream {
	return &fakeInvokeStream{events: []*toolv1.ToolEvent{
		{Event: &toolv1.ToolEvent_Result{Result: &toolv1.ToolResult{Payload: payload}}},
	}}
}

// errorStream builds a fakeInvokeStream whose only event is a terminal
// provider-emitted error.
func errorStream(toolErr *toolv1.ToolError) *fakeInvokeStream {
	return &fakeInvokeStream{events: []*toolv1.ToolEvent{
		{Event: &toolv1.ToolEvent_Error{Error: toolErr}},
	}}
}

// fakeEvents is an in-memory EventSink: sequence starts at 1 and
// increments per successful append, matching statebackend's own
// AUTOINCREMENT semantics closely enough for these tests (see
// determinism.md — tests here assert on Sequence, never on wall-clock
// order). failAt, if non-zero, makes the failAt-th AppendEvent call
// (1-indexed) return errInjected instead of succeeding.
type fakeEvents struct {
	mu     sync.Mutex
	seq    int64
	events []statebackend.Event
	failAt int
}

var errInjected = errors.New("fakeEvents: injected failure")

func (f *fakeEvents) AppendEvent(_ context.Context, ev statebackend.Event) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.events) + 1
	if f.failAt != 0 && n == f.failAt {
		return 0, errInjected
	}
	f.seq++
	ev.Sequence = f.seq
	f.events = append(f.events, ev)
	return f.seq, nil
}

func (f *fakeEvents) snapshot() []statebackend.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]statebackend.Event, len(f.events))
	copy(out, f.events)
	return out
}

// fakeResolver is a hand-written interactive.Resolver fake, scripted per
// call in declaration order via responses/errs. Calling it more times
// than scripted panics — a bug in the test, not a legitimate scenario.
type fakeResolver struct {
	mu        sync.Mutex
	calls     []interactive.Request
	responses []interactive.Response
	errs      []error

	// onCall, if set, is invoked synchronously inside Resolve before
	// returning — TestExecuteInteractive_Sequential's overlap check
	// hangs off this.
	onCall func(callIndex int)
}

var _ interactive.Resolver = (*fakeResolver)(nil)

func (f *fakeResolver) Resolve(_ context.Context, req interactive.Request) (interactive.Response, error) {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, req)
	f.mu.Unlock()

	if f.onCall != nil {
		f.onCall(idx)
	}

	if idx >= len(f.responses) {
		panic("fakeResolver: Resolve called more times than scripted")
	}
	return f.responses[idx], f.errs[idx]
}

// mustStruct builds a *structpb.Struct from m, failing the test on
// error — every literal this helper is given is a valid JSON value by
// construction, so an error here is a test-authoring bug.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("mustStruct: %v", err)
	}
	return s
}

// newToolHandle builds a providercatalog.ToolHandle for provider/tool
// with the given ConcurrencySpec and output schema, wired to client.
func newToolHandle(provider, tool string, kind toolv1.ToolKind, spec *toolv1.ConcurrencySpec, outputSchema *schemav1.Schema, client toolv1.ToolServiceClient) providercatalog.ToolHandle {
	return providercatalog.ToolHandle{
		Provider: provider,
		Producer: &commonv1.ProducerRef{
			Name:     provider,
			Version:  "1.0.0",
			Category: commonv1.Category_CATEGORY_TOOL,
		},
		Schema: &toolv1.ToolSchema{
			Name:         tool,
			Kind:         kind,
			OutputSchema: outputSchema,
			Concurrency:  spec,
		},
		Client: client,
	}
}

// newCall builds a Call whose ToolCall has id/tool_name/arguments set,
// against handle.
func newCall(id, tool string, args *structpb.Struct, handle providercatalog.ToolHandle) Call {
	return Call{
		Call: &toolv1.ToolCall{
			Id:        id,
			ToolName:  tool,
			Arguments: args,
		},
		Handle: handle,
	}
}

// testScheduler builds a Scheduler wired to fresh fakes, returning both
// for assertions. cfg is a caller-supplied base (Interactive/Breaker/
// SerializeAll/DefaultTimeout); Events/Logger/Telemetry are always
// overwritten.
func testScheduler(t *testing.T, cfg Config) (*Scheduler, *fakeEvents) {
	t.Helper()
	events := &fakeEvents{}
	cfg.Events = events
	cfg.Logger = testLogger(t)
	return New(cfg), events
}
