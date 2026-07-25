package model

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc/metadata"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// fakeServerStream is a hand-written modelv1.ModelService_StreamCompletionServer
// fake (go-testing.md: fakes, not mocking frameworks) — enough of
// grpc.ServerStream to exercise Sink's own logic without a real network
// round trip; server_test.go covers the real bufconn path end to end.
type fakeServerStream struct {
	ctx context.Context //nolint:containedctx // test fixture standing in for the real grpc.ServerStream, whose own Context() is likewise just a stored field under the hood.

	mu      sync.Mutex
	sent    []*modelv1.StreamEvent
	sendErr error
}

func newFakeServerStream(ctx context.Context) *fakeServerStream {
	return &fakeServerStream{ctx: ctx}
}

func (f *fakeServerStream) Send(ev *modelv1.StreamEvent) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ev)
	return nil
}

func (f *fakeServerStream) events() []*modelv1.StreamEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*modelv1.StreamEvent(nil), f.sent...)
}

func (f *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeServerStream) SetTrailer(metadata.MD)       {}
func (f *fakeServerStream) Context() context.Context     { return f.ctx }
func (f *fakeServerStream) SendMsg(any) error            { return nil }
func (f *fakeServerStream) RecvMsg(any) error            { return nil }

var _ modelv1.ModelService_StreamCompletionServer = (*fakeServerStream)(nil)

func TestSink_EventVariants(t *testing.T) {
	t.Parallel()

	stream := newFakeServerStream(t.Context())
	sink := newSink(stream)

	if err := sink.TextDelta("hello"); err != nil {
		t.Fatalf("TextDelta() = %v, want nil", err)
	}
	if err := sink.ThinkingDelta("reasoning"); err != nil {
		t.Fatalf("ThinkingDelta() = %v, want nil", err)
	}
	if err := sink.ThinkingSignature([]byte("sig")); err != nil {
		t.Fatalf("ThinkingSignature() = %v, want nil", err)
	}
	if err := sink.RedactedThinking([]byte("encrypted")); err != nil {
		t.Fatalf("RedactedThinking() = %v, want nil", err)
	}
	if err := sink.ToolCallStart("call-1", "read_file"); err != nil {
		t.Fatalf("ToolCallStart() = %v, want nil", err)
	}
	if err := sink.ToolCallDelta("call-1", `{"path":`); err != nil {
		t.Fatalf("ToolCallDelta() = %v, want nil", err)
	}
	if err := sink.ToolCallDone("call-1"); err != nil {
		t.Fatalf("ToolCallDone() = %v, want nil", err)
	}
	reasoning := int64(5)
	if err := sink.Usage(Usage{InputTokens: 10, OutputTokens: 20, ReasoningTokens: &reasoning}); err != nil {
		t.Fatalf("Usage() = %v, want nil", err)
	}

	events := stream.events()
	if len(events) != 8 {
		t.Fatalf("len(events) = %d, want 8", len(events))
	}
	if events[0].GetTextDelta().GetText() != "hello" {
		t.Errorf("events[0].TextDelta.Text = %q, want %q", events[0].GetTextDelta().GetText(), "hello")
	}
	// RedactedThinking carries the vendor's bytes through untouched — the
	// kernel round-trips them verbatim or the vendor rejects the next turn.
	if got := string(events[3].GetRedactedThinking().GetData()); got != "encrypted" {
		t.Errorf("events[3].RedactedThinking.Data = %q, want %q", got, "encrypted")
	}
	if events[7].GetUsage().GetReasoningTokens() != 5 {
		t.Errorf("events[7].Usage.ReasoningTokens = %d, want 5", events[7].GetUsage().GetReasoningTokens())
	}
}

func TestSink_StopIsTerminal(t *testing.T) {
	t.Parallel()

	stream := newFakeServerStream(t.Context())
	sink := newSink(stream)

	if err := sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, ""); err != nil {
		t.Fatalf("first Stop() = %v, want nil", err)
	}
	if err := sink.TextDelta("too late"); !errors.Is(err, ErrStreamAlreadyTerminated) {
		t.Fatalf("TextDelta() after Stop() = %v, want ErrStreamAlreadyTerminated", err)
	}
	if err := sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, ""); !errors.Is(err, ErrStreamAlreadyTerminated) {
		t.Fatalf("second Stop() = %v, want ErrStreamAlreadyTerminated", err)
	}

	events := stream.events()
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].GetStop().GetReason() != modelv1.StopReason_STOP_REASON_END_TURN {
		t.Errorf("events[0].Stop.Reason = %v, want STOP_REASON_END_TURN", events[0].GetStop().GetReason())
	}
}

func TestSink_StopMatchedStopSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		reason                modelv1.StopReason
		matchedStopSequence   string
		wantMatchedSequence   string
		wantSequenceSetOnWire bool
	}{
		{
			name:                  "stop sequence reason carries the matched sequence",
			reason:                modelv1.StopReason_STOP_REASON_STOP_SEQUENCE,
			matchedStopSequence:   "</done>",
			wantMatchedSequence:   "</done>",
			wantSequenceSetOnWire: true,
		},
		{
			name:                  "end_turn reason never carries a matched sequence",
			reason:                modelv1.StopReason_STOP_REASON_END_TURN,
			matchedStopSequence:   "</done>",
			wantSequenceSetOnWire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stream := newFakeServerStream(t.Context())
			sink := newSink(stream)
			if err := sink.Stop(tt.reason, tt.matchedStopSequence); err != nil {
				t.Fatalf("Stop() = %v, want nil", err)
			}
			events := stream.events()
			stop := events[0].GetStop()
			if stop.MatchedStopSequence != nil != tt.wantSequenceSetOnWire {
				t.Errorf("MatchedStopSequence set = %v, want %v", stop.MatchedStopSequence != nil, tt.wantSequenceSetOnWire)
			}
			if tt.wantSequenceSetOnWire && stop.GetMatchedStopSequence() != tt.wantMatchedSequence {
				t.Errorf("MatchedStopSequence = %q, want %q", stop.GetMatchedStopSequence(), tt.wantMatchedSequence)
			}
		})
	}
}

func TestSink_ErrorIsTerminal(t *testing.T) {
	t.Parallel()

	stream := newFakeServerStream(t.Context())
	sink := newSink(stream)

	modelErr := &Error{
		Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED,
		Message:  "vendor overloaded",
	}
	if err := sink.Error(modelErr); err != nil {
		t.Fatalf("Error() = %v, want nil", err)
	}
	if err := sink.Stop(modelv1.StopReason_STOP_REASON_END_TURN, ""); !errors.Is(err, ErrStreamAlreadyTerminated) {
		t.Fatalf("Stop() after Error() = %v, want ErrStreamAlreadyTerminated", err)
	}

	events := stream.events()
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if got := events[0].GetError().GetError().GetMessage(); got != "vendor overloaded" {
		t.Errorf("events[0].Error.Error.Message = %q, want %q", got, "vendor overloaded")
	}
}

func TestSink_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stream := newFakeServerStream(ctx)
	sink := newSink(stream)

	if err := sink.TextDelta("too late"); !errors.Is(err, context.Canceled) {
		t.Fatalf("TextDelta() on cancelled context = %v, want context.Canceled", err)
	}
	if len(stream.events()) != 0 {
		t.Errorf("events sent on a cancelled context, want none")
	}
}

func TestSink_Context(t *testing.T) {
	t.Parallel()

	stream := newFakeServerStream(t.Context())
	sink := newSink(stream)
	if sink.Context() != stream.ctx {
		t.Errorf("Context() did not return the wrapped stream's context")
	}
}
