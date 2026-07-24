package frontend_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/frontend"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
)

// attachClient starts a Service wrapping provider and opens one Attach
// stream against it, mirroring how the kernel — the FrontendServiceClient
// for this RPC, per doc.go's "Wire direction" — would call in.
func attachClient(t *testing.T, provider frontend.Provider) frontendv1.FrontendService_AttachClient {
	t.Helper()

	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))
	stream, err := client.Attach(t.Context())
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	return stream
}

func userMessageEvent(sessionID, text string) *frontendv1.ClientEvent {
	return &frontendv1.ClientEvent{
		SessionId: sessionID,
		Event: &frontendv1.ClientEvent_UserMessage_{
			UserMessage: &frontendv1.ClientEvent_UserMessage{
				Content: []*contentv1.ContentBlock{{
					Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: text}},
				}},
			},
		},
	}
}

func interruptEvent() *frontendv1.ClientEvent {
	return &frontendv1.ClientEvent{
		SessionId: "sess-1",
		Event:     &frontendv1.ClientEvent_Interrupt_{Interrupt: &frontendv1.ClientEvent_Interrupt{}},
	}
}

// TestAttach_SessionDemux sends ClientEvents for two different sessions
// interleaved on the one Attach stream and checks each is dispatched with
// its own session_id, and each reply is tagged back with the matching
// session_id — frontend-protocol.md's per-session multiplexing over one
// connection-scoped stream.
func TestAttach_SessionDemux(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	seen := map[string]int{}

	provider := &fakeProvider{
		handleEventFunc: func(_ context.Context, event frontend.ClientEvent, emit frontend.Emitter) error {
			mu.Lock()
			seen[event.SessionID]++
			mu.Unlock()

			um, ok := event.Payload.(frontend.UserMessage)
			if !ok {
				return nil
			}
			return emit.Emit(frontend.ServerEvent{
				SessionID: event.SessionID,
				Payload:   frontend.StreamDelta{TargetID: "t", Text: um.Content[0].GetText().GetText()},
			})
		},
	}

	stream := attachClient(t, provider)

	if err := stream.Send(userMessageEvent("sess-a", "hello-a")); err != nil {
		t.Fatalf("Send(sess-a) error = %v", err)
	}
	if err := stream.Send(userMessageEvent("sess-b", "hello-b")); err != nil {
		t.Fatalf("Send(sess-b) error = %v", err)
	}

	gotA, gotB := false, false
	for range 2 {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		delta := resp.GetStreamDelta()
		if delta == nil {
			t.Fatalf("Recv() = %v, want stream_delta", resp)
		}
		switch resp.GetSessionId() {
		case "sess-a":
			if delta.GetText() != "hello-a" {
				t.Errorf("sess-a delta text = %q, want hello-a", delta.GetText())
			}
			gotA = true
		case "sess-b":
			if delta.GetText() != "hello-b" {
				t.Errorf("sess-b delta text = %q, want hello-b", delta.GetText())
			}
			gotB = true
		default:
			t.Errorf("unexpected session_id %q", resp.GetSessionId())
		}
	}
	if !gotA || !gotB {
		t.Errorf("did not receive replies for both sessions: gotA=%v gotB=%v", gotA, gotB)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen["sess-a"] != 1 || seen["sess-b"] != 1 {
		t.Errorf("HandleEvent call counts = %v, want 1 for each session", seen)
	}
}

// TestAttach_RequestIDCorrelation sends a CreateSession control event and
// checks the request_id the Provider echoes back arrives unchanged on the
// ServerEvent that answers it.
func TestAttach_RequestIDCorrelation(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		handleEventFunc: func(_ context.Context, event frontend.ClientEvent, emit frontend.Emitter) error {
			cs, ok := event.Payload.(frontend.CreateSession)
			if !ok {
				return nil
			}
			reqID := cs.RequestID
			return emit.Emit(frontend.ServerEvent{
				SessionID: "new-sess",
				RequestID: &reqID,
				Payload:   frontend.SessionCreated{},
			})
		},
	}

	stream := attachClient(t, provider)

	if err := stream.Send(&frontendv1.ClientEvent{
		Event: &frontendv1.ClientEvent_CreateSession_{
			CreateSession: &frontendv1.ClientEvent_CreateSession{RequestId: "req-42"},
		},
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if resp.GetSessionCreated() == nil {
		t.Fatalf("Recv() = %v, want session_created", resp)
	}
	if resp.GetRequestId() != "req-42" {
		t.Errorf("RequestId = %q, want req-42", resp.GetRequestId())
	}
}

// TestAttach_InBandErrorKeepsStreamOpen checks that an ordinary error
// returned from HandleEvent surfaces as an in-band ServerEvent.error and
// the stream remains usable for subsequent events afterward — doc.go's
// "Error handling is two distinct paths, not one".
func TestAttach_InBandErrorKeepsStreamOpen(t *testing.T) {
	t.Parallel()

	calls := 0
	provider := &fakeProvider{
		handleEventFunc: func(_ context.Context, event frontend.ClientEvent, emit frontend.Emitter) error {
			calls++
			if calls == 1 {
				return errors.New("recoverable failure")
			}
			return emit.Emit(frontend.ServerEvent{SessionID: event.SessionID, Payload: frontend.StreamDelta{Text: "ok"}})
		},
	}

	stream := attachClient(t, provider)

	if err := stream.Send(interruptEvent()); err != nil {
		t.Fatalf("Send() (first) error = %v", err)
	}
	resp1, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv() (first) error = %v", err)
	}
	errEvent := resp1.GetError()
	if errEvent == nil {
		t.Fatalf("Recv() (first) = %v, want error", resp1)
	}
	if got := errEvent.GetError().GetCategory(); got != frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNKNOWN {
		t.Errorf("category = %v, want UNKNOWN", got)
	}

	// The stream stays open: a second event is still handled normally.
	if err := stream.Send(interruptEvent()); err != nil {
		t.Fatalf("Send() (second) error = %v", err)
	}
	resp2, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv() (second) error = %v", err)
	}
	if got := resp2.GetStreamDelta().GetText(); got != "ok" {
		t.Errorf("second response = %q, want ok", got)
	}
}

// TestAttach_FatalClosesStream checks that frontend.Fatal from HandleEvent
// closes the Attach RPC with a gRPC status instead of reporting in-band.
func TestAttach_FatalClosesStream(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		handleEventFunc: func(context.Context, frontend.ClientEvent, frontend.Emitter) error {
			return frontend.Fatal(errors.New("plugin process is dying"))
		},
	}

	stream := attachClient(t, provider)

	if err := stream.Send(interruptEvent()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	_, err := stream.Recv()
	if err == nil {
		t.Fatalf("Recv() = nil error, want the stream to close after a Fatal HandleEvent")
	}
	if errors.Is(err, io.EOF) {
		t.Errorf("Recv() = io.EOF, want a non-EOF error carrying the fatal condition")
	}
}

// TestAttach_InvalidClientEvent checks that a session-scoped ClientEvent
// arriving with an empty session_id is rejected in-band as
// FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT without ever reaching the
// Provider — frontend-protocol.md's error taxonomy.
func TestAttach_InvalidClientEvent(t *testing.T) {
	t.Parallel()

	called := false
	provider := &fakeProvider{
		handleEventFunc: func(context.Context, frontend.ClientEvent, frontend.Emitter) error {
			called = true
			return nil
		},
	}

	stream := attachClient(t, provider)

	if err := stream.Send(&frontendv1.ClientEvent{
		Event: &frontendv1.ClientEvent_UserMessage_{UserMessage: &frontendv1.ClientEvent_UserMessage{}},
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	errEvent := resp.GetError()
	if errEvent == nil {
		t.Fatalf("Recv() = %v, want error", resp)
	}
	if got := errEvent.GetError().GetCategory(); got != frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT {
		t.Errorf("category = %v, want INVALID_CLIENT_EVENT", got)
	}
	if called {
		t.Errorf("HandleEvent was called for a malformed ClientEvent; want it skipped")
	}
}

// TestAttach_UnicastNotBroadcast opens two independent Attach connections
// against the same Service and checks that a reply emitted on one
// connection is never observed on the other — frontend-protocol.md's
// "Backfill is unicast to the attaching stream only, never broadcast",
// generalized to this package's own connection-scoped Emitter: nothing in
// this SDK fans an emitted ServerEvent out beyond the *connection that
// produced it.
func TestAttach_UnicastNotBroadcast(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		handleEventFunc: func(_ context.Context, event frontend.ClientEvent, emit frontend.Emitter) error {
			return emit.Emit(frontend.ServerEvent{SessionID: event.SessionID, Payload: frontend.StreamDelta{Text: "reply"}})
		},
	}

	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))

	streamA, err := client.Attach(t.Context())
	if err != nil {
		t.Fatalf("Attach() (A) error = %v", err)
	}
	streamB, err := client.Attach(t.Context())
	if err != nil {
		t.Fatalf("Attach() (B) error = %v", err)
	}

	if err := streamA.Send(interruptEvent()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	respA, err := streamA.Recv()
	if err != nil {
		t.Fatalf("Recv() (A) error = %v", err)
	}
	if got := respA.GetStreamDelta().GetText(); got != "reply" {
		t.Fatalf("stream A reply = %q, want reply", got)
	}

	// stream B must never observe A's reply. Bound the "nothing happened"
	// wait with a short, overridable timeout rather than blocking forever.
	recvB := make(chan struct{})
	go func() {
		_, _ = streamB.Recv()
		close(recvB)
	}()
	select {
	case <-recvB:
		t.Errorf("stream B received an event that was only ever emitted on stream A")
	case <-time.After(150 * time.Millisecond):
		// Expected: B never receives anything.
	}
}
