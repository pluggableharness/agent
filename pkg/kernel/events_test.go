package kernel_test

import (
	"sync"
	"testing"
	"time"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestClient_ReadEvents_receivesEvents(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{
		readEventsFunc: func(req *kernelv1.ReadEventsRequest, stream kernelv1.KernelCallbackService_ReadEventsServer) error {
			if req.GetSessionId() != "session-01" {
				t.Errorf("server received session_id %q, want session-01", req.GetSessionId())
			}
			if err := stream.Send(&kernelv1.StoredEvent{Sequence: 1, Id: "evt-01"}); err != nil {
				return err
			}
			if err := stream.Send(&kernelv1.StoredEvent{Sequence: 2, Id: "evt-02"}); err != nil {
				return err
			}
			return nil
		},
	}
	c := newTestClient(t, srv)

	var mu sync.Mutex
	var received []*kernelv1.StoredEvent
	sub, err := c.ReadEvents(t.Context(), &kernelv1.ReadEventsRequest{SessionId: "session-01"}, func(ev *kernelv1.StoredEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, ev)
	})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0].GetSequence() != 1 || received[1].GetSequence() != 2 {
		t.Fatalf("received = %+v, want two events with sequence 1, 2", received)
	}
}

func TestClient_ReadEvents_closeStopsReceiving(t *testing.T) {
	t.Parallel()

	streamStarted := make(chan struct{})
	srv := &fakeServer{
		readEventsFunc: func(_ *kernelv1.ReadEventsRequest, stream kernelv1.KernelCallbackService_ReadEventsServer) error {
			close(streamStarted)
			<-stream.Context().Done()
			return nil
		},
	}
	c := newTestClient(t, srv)

	sub, err := c.ReadEvents(t.Context(), &kernelv1.ReadEventsRequest{SessionId: "session-01"}, func(*kernelv1.StoredEvent) {})
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}

	select {
	case <-streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the fake server's ReadEvents to start")
	}

	closed := make(chan struct{})
	go func() {
		_ = sub.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
}
