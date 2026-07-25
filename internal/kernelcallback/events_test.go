package kernelcallback

import (
	"context"
	"errors"
	"testing"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/sessionstate"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// errSendFailed is the sentinel erroringReadEventsStream.Send returns, so
// TestServer_ReadEvents_sendFailurePropagates can assert ReadEvents
// propagates the exact stream error rather than wrapping or replacing it.
var errSendFailed = errors.New("fake stream: send failed")

// fakeReadEventsStream is a hand-written fake of
// kernelv1.KernelCallbackService_ReadEventsServer (go-testing.md: fakes,
// not mocking frameworks), mirroring eventbus_test.go's
// fakeSubscribeStream for the StoredEvent-shaped stream.
type fakeReadEventsStream struct {
	ctx  context.Context
	sent []*kernelv1.StoredEvent
}

func newFakeReadEventsStream(ctx context.Context) *fakeReadEventsStream {
	return &fakeReadEventsStream{ctx: ctx}
}

func (f *fakeReadEventsStream) Send(ev *kernelv1.StoredEvent) error {
	f.sent = append(f.sent, ev)
	return nil
}

// erroringReadEventsStream is a fakeReadEventsStream variant whose every
// Send call fails immediately, for exercising ReadEvents' stream.Send
// failure branch.
type erroringReadEventsStream struct {
	*fakeReadEventsStream
	sendErr error
}

func (f *erroringReadEventsStream) Send(*kernelv1.StoredEvent) error {
	return f.sendErr
}

func (f *fakeReadEventsStream) Context() context.Context     { return f.ctx }
func (f *fakeReadEventsStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeReadEventsStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeReadEventsStream) SetTrailer(metadata.MD)       {}
func (f *fakeReadEventsStream) SendMsg(any) error            { return nil }
func (f *fakeReadEventsStream) RecvMsg(any) error            { return nil }

func TestServer_ReadEvents_authorizationFailure(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	stream := newFakeReadEventsStream(t.Context())

	err := f.server.ReadEvents(&kernelv1.ReadEventsRequest{SessionId: "no-such-session"}, stream)
	assertCode(t, err, codes.PermissionDenied)
}

func TestServer_ReadEvents_streamsFilteredOrderedResults(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, ok := f.sessions.Get(sessionID)
	if !ok {
		t.Fatalf("test setup: session %q not registered live", sessionID)
	}

	kinds := []kernelv1.EventKind{
		kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		kernelv1.EventKind_EVENT_KIND_TOOL_RESULT,
		kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
	}
	for _, kind := range kinds {
		if _, err := live.Emit(t.Context(), sessionstate.EmitRecord{
			Producer:      testProducer(),
			Kind:          kind,
			SchemaVersion: "1",
			Payload:       []byte("x"),
		}); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}

	stream := newFakeReadEventsStream(t.Context())
	err := f.server.ReadEvents(&kernelv1.ReadEventsRequest{
		SessionId: sessionID,
		Kinds:     []kernelv1.EventKind{kernelv1.EventKind_EVENT_KIND_TOOL_CALL},
	}, stream)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}

	if len(stream.sent) != 2 {
		t.Fatalf("streamed %d events, want 2 (filtered to EVENT_KIND_TOOL_CALL)", len(stream.sent))
	}
	if stream.sent[0].GetSequence() != 1 || stream.sent[1].GetSequence() != 3 {
		t.Errorf("streamed sequences = [%d %d], want [1 3]", stream.sent[0].GetSequence(), stream.sent[1].GetSequence())
	}
	for _, ev := range stream.sent {
		if ev.GetKind() != kernelv1.EventKind_EVENT_KIND_TOOL_CALL {
			t.Errorf("streamed event kind = %v, want EVENT_KIND_TOOL_CALL", ev.GetKind())
		}
		if ev.GetId() == "" {
			t.Error("streamed event Id is empty")
		}
		if ev.GetProducer().GetName() != testProducer().GetName() {
			t.Errorf("streamed event Producer.Name = %q, want %q", ev.GetProducer().GetName(), testProducer().GetName())
		}
	}
}

func TestServer_ReadEvents_fromSequenceAndLimit(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, _ := f.sessions.Get(sessionID)
	for range 5 {
		if _, err := live.Emit(t.Context(), sessionstate.EmitRecord{
			Producer:      testProducer(),
			Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
			SchemaVersion: "1",
			Payload:       []byte("x"),
		}); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}

	from := int64(2)
	limit := int32(2)
	stream := newFakeReadEventsStream(t.Context())
	err := f.server.ReadEvents(&kernelv1.ReadEventsRequest{
		SessionId:    sessionID,
		FromSequence: &from,
		Limit:        &limit,
	}, stream)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("streamed %d events, want 2 (limit)", len(stream.sent))
	}
	if stream.sent[0].GetSequence() != 2 || stream.sent[1].GetSequence() != 3 {
		t.Errorf("streamed sequences = [%d %d], want [2 3]", stream.sent[0].GetSequence(), stream.sent[1].GetSequence())
	}
}

func TestServer_ReadEvents_queryFailure(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, ok := f.sessions.Get(sessionID)
	if !ok {
		t.Fatalf("test setup: session %q not registered live", sessionID)
	}
	// As in TestServer_Emit_liveWriteFailure: Close the underlying session
	// directly, leaving it in the live table and still granted, to
	// exercise ReadEvents' own query-failure branch (codes.Internal).
	if err := live.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream := newFakeReadEventsStream(t.Context())
	err := f.server.ReadEvents(&kernelv1.ReadEventsRequest{SessionId: sessionID}, stream)
	assertCode(t, err, codes.Internal)
}

func TestServer_ReadEvents_sendFailurePropagates(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	sessionID, release := newLiveSession(t, f, bounds.Limits{})
	t.Cleanup(release)

	live, _ := f.sessions.Get(sessionID)
	if _, err := live.Emit(t.Context(), sessionstate.EmitRecord{
		Producer:      testProducer(),
		Kind:          kernelv1.EventKind_EVENT_KIND_TOOL_CALL,
		SchemaVersion: "1",
		Payload:       []byte("x"),
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	wantErr := errSendFailed
	stream := &erroringReadEventsStream{fakeReadEventsStream: newFakeReadEventsStream(t.Context()), sendErr: wantErr}
	err := f.server.ReadEvents(&kernelv1.ReadEventsRequest{SessionId: sessionID}, stream)
	if !errors.Is(err, wantErr) {
		t.Errorf("ReadEvents error = %v, want wrapping %v", err, wantErr)
	}
}
