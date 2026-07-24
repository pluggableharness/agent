package widget

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/metadata"

	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// fakeAttachStream is a hand-written widgetv1.WidgetService_AttachServer
// fake (go-testing.md: fakes, not mocking frameworks), used to exercise
// UpdateSender.Send's branches that a real bufconn round trip can't
// deterministically reach — in particular a stream-level send failure
// that is not itself caused by context cancellation. server_test.go
// covers Send's ordinary and cancellation-driven paths through a real
// gRPC round trip; this file is the one place in this package that needs
// direct access to the unexported newUpdateSender constructor.
type fakeAttachStream struct {
	ctx     context.Context
	sendErr error
}

func (f *fakeAttachStream) Send(*widgetv1.WidgetUpdate) error { return f.sendErr }
func (f *fakeAttachStream) SetHeader(metadata.MD) error       { return nil }
func (f *fakeAttachStream) SendHeader(metadata.MD) error      { return nil }
func (f *fakeAttachStream) SetTrailer(metadata.MD)            {}
func (f *fakeAttachStream) Context() context.Context          { return f.ctx }
func (f *fakeAttachStream) SendMsg(any) error                 { return nil }
func (f *fakeAttachStream) RecvMsg(any) error                 { return nil }

var _ widgetv1.WidgetService_AttachServer = (*fakeAttachStream)(nil)

func TestUpdateSender_Send_success(t *testing.T) {
	t.Parallel()

	stream := &fakeAttachStream{ctx: t.Context()}
	sender := newUpdateSender(stream.ctx, stream)

	if err := sender.Send(Update{}); err != nil {
		t.Errorf("Send() = %v, want nil", err)
	}
}

func TestUpdateSender_Send_ctxAlreadyCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stream := &fakeAttachStream{ctx: ctx}
	sender := newUpdateSender(ctx, stream)

	if err := sender.Send(Update{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Send() = %v, want context.Canceled", err)
	}
}

// TestUpdateSender_Send_nonCancellationError covers a stream-level Send
// failure unrelated to cancellation (a broken pipe, a marshal failure) —
// UpdateSender.Send must wrap and return it distinguishably from
// context.Canceled, not silently swallow it as "probably cancellation."
func TestUpdateSender_Send_nonCancellationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection reset")
	stream := &fakeAttachStream{ctx: t.Context(), sendErr: wantErr}
	sender := newUpdateSender(stream.ctx, stream)

	err := sender.Send(Update{})
	if !errors.Is(err, wantErr) {
		t.Errorf("Send() = %v, want wrapping %v", err, wantErr)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("Send() = %v, want not context.Canceled", err)
	}
}
