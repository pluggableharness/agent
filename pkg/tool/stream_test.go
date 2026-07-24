package tool

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"google.golang.org/grpc/metadata"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// fakeInvokeServerStream is a hand-written fake of
// toolv1.ToolService_InvokeServer (grpc.ServerStreamingServer[InvokeResponse]),
// per .claude/rules/go-testing.md's "fakes, not mocking frameworks" rule.
type fakeInvokeServerStream struct {
	ctx     context.Context
	sendErr error

	mu   sync.Mutex
	sent []*toolv1.InvokeResponse
}

func newFakeInvokeServerStream(ctx context.Context) *fakeInvokeServerStream {
	return &fakeInvokeServerStream{ctx: ctx}
}

func (f *fakeInvokeServerStream) Send(r *toolv1.InvokeResponse) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, r)
	return nil
}

func (f *fakeInvokeServerStream) events() []*toolv1.InvokeResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*toolv1.InvokeResponse(nil), f.sent...)
}

func (f *fakeInvokeServerStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeInvokeServerStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeInvokeServerStream) SetTrailer(metadata.MD)       {}
func (f *fakeInvokeServerStream) Context() context.Context     { return f.ctx }
func (f *fakeInvokeServerStream) SendMsg(m any) error {
	return f.Send(m.(*toolv1.InvokeResponse))
}
func (f *fakeInvokeServerStream) RecvMsg(any) error { return io.EOF }

var _ toolv1.ToolService_InvokeServer = (*fakeInvokeServerStream)(nil)

func TestStreamSendTerminalClosesStream(t *testing.T) {
	t.Parallel()

	f := newFakeInvokeServerStream(t.Context())
	s := newStream(f)

	if err := s.Send(NewOutputChunkEvent(OutputStreamStdout, []byte("a"))); err != nil {
		t.Fatalf("Send(output_chunk): %v", err)
	}
	if s.closedTerminal() {
		t.Fatal("closedTerminal() = true before any terminal event")
	}

	if err := s.Send(NewResultEvent(map[string]any{"ok": true})); err != nil {
		t.Fatalf("Send(result): %v", err)
	}
	if !s.closedTerminal() {
		t.Fatal("closedTerminal() = false after a result event")
	}

	// A second terminal event — even a different one — after the first
	// MUST be rejected: exactly one of result/error closes the stream.
	err := s.Send(NewErrorEvent(&Error{Category: ErrorCategoryUnknown, Message: "too late"}))
	if !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("second terminal Send() error = %v, want wrapping %v", err, ErrStreamClosed)
	}

	if got := len(f.events()); got != 2 {
		t.Errorf("events sent = %d, want 2 (the rejected send must not reach the wire)", got)
	}
}

func TestStreamSendNilEvent(t *testing.T) {
	t.Parallel()

	s := newStream(newFakeInvokeServerStream(t.Context()))
	if err := s.Send(nil); !errors.Is(err, ErrNilEvent) {
		t.Errorf("Send(nil) error = %v, want wrapping %v", err, ErrNilEvent)
	}
}

func TestStreamDuplicateExitStatusRejected(t *testing.T) {
	t.Parallel()

	s := newStream(newFakeInvokeServerStream(t.Context()))

	if err := s.Send(NewExitStatusEvent(0, nil)); err != nil {
		t.Fatalf("first exit_status Send(): %v", err)
	}
	err := s.Send(NewExitStatusEvent(1, nil))
	if !errors.Is(err, ErrDuplicateExitStatus) {
		t.Fatalf("second exit_status Send() error = %v, want wrapping %v", err, ErrDuplicateExitStatus)
	}
}

func TestStreamResultAfterCancelRejected(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	f := newFakeInvokeServerStream(ctx)
	s := newStream(f)

	cancel() // simulate the kernel closing the stream mid-call.

	err := s.Send(NewResultEvent(map[string]any{"ok": true}))
	if !errors.Is(err, ErrResultAfterCancel) {
		t.Fatalf("Send(result) after cancel error = %v, want wrapping %v", err, ErrResultAfterCancel)
	}

	// A best-effort partial-mutation report MUST still be sendable after
	// cancellation — only a synthesized success result is refused.
	if err := s.Send(NewPartialResultEvent(map[string]any{"partial": true})); err != nil {
		t.Fatalf("Send(partial_result) after cancel: %v", err)
	}
	if err := s.Send(NewErrorEvent(&Error{Category: ErrorCategoryCancelled, Message: "cancelled"})); err != nil {
		t.Fatalf("Send(error, cancelled) after cancel: %v", err)
	}
	if !s.closedTerminal() {
		t.Error("closedTerminal() = false after a cancelled-category error event")
	}
}

func TestStreamSendPropagatesTransportError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("broken pipe")
	f := newFakeInvokeServerStream(t.Context())
	f.sendErr = wantErr
	s := newStream(f)

	err := s.Send(NewOutputChunkEvent(OutputStreamStdout, []byte("x")))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Send() error = %v, want wrapping %v", err, wantErr)
	}
	if s.closedTerminal() {
		t.Error("closedTerminal() = true after a non-terminal event's Send failed")
	}
}

func TestStreamSendPreservesOrdering(t *testing.T) {
	t.Parallel()

	f := newFakeInvokeServerStream(t.Context())
	s := newStream(f)

	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_ = s.Send(NewProgressEvent("step", nil))
			_ = i
		}(i)
	}
	wg.Wait()

	if got := len(f.events()); got != n {
		t.Fatalf("events sent = %d, want %d (concurrent Send calls must not race the transport)", got, n)
	}
}

func TestStreamContext(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newFakeInvokeServerStream(ctx)
	s := newStream(f)
	if s.Context() != ctx {
		t.Error("Context() did not return the underlying gRPC stream's context")
	}
}
