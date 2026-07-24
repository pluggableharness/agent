package kernel_test

import (
	"sync"
	"testing"
	"time"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func TestClient_Publish(t *testing.T) {
	t.Parallel()

	var gotReq *kernelv1.PublishRequest
	srv := &fakeServer{
		publishFunc: func(req *kernelv1.PublishRequest) (*kernelv1.PublishResult, error) {
			gotReq = req
			return &kernelv1.PublishResult{Topic: "plugin.tool.github.file_changed"}, nil
		},
	}
	c := newTestClient(t, srv)

	topic, err := c.Publish(t.Context(), "file_changed", []byte("data"), "text/plain", "1")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if topic != "plugin.tool.github.file_changed" {
		t.Errorf("Publish() topic = %q, want plugin.tool.github.file_changed", topic)
	}
	if gotReq.GetEventType() != "file_changed" || gotReq.GetPayloadType() != "text/plain" || gotReq.GetSchemaVersion() != "1" {
		t.Errorf("server received %+v, want event_type=file_changed payload_type=text/plain schema_version=1", gotReq)
	}
}

func TestClient_Subscribe_receivesEvents(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{
		subscribeFunc: func(req *kernelv1.SubscribeRequest, stream kernelv1.KernelCallbackService_SubscribeServer) error {
			if len(req.GetTopicFilters()) != 1 || req.GetTopicFilters()[0] != "plugin.tool.github.*" {
				t.Errorf("server received filters %v, want [plugin.tool.github.*]", req.GetTopicFilters())
			}
			if err := stream.Send(&kernelv1.BusEvent{Topic: "plugin.tool.github.file_changed"}); err != nil {
				return err
			}
			<-stream.Context().Done()
			return nil
		},
	}
	c := newTestClient(t, srv)

	var mu sync.Mutex
	var received []*kernelv1.BusEvent
	sub, err := c.Subscribe(t.Context(), []string{"plugin.tool.github.*"}, func(ev *kernelv1.BusEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, ev)
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].GetTopic() != "plugin.tool.github.file_changed" {
		t.Fatalf("received = %+v, want one event on plugin.tool.github.file_changed", received)
	}
}

func TestClient_Subscribe_closeStopsReceiving(t *testing.T) {
	t.Parallel()

	streamStarted := make(chan struct{})
	srv := &fakeServer{
		subscribeFunc: func(_ *kernelv1.SubscribeRequest, stream kernelv1.KernelCallbackService_SubscribeServer) error {
			close(streamStarted)
			<-stream.Context().Done()
			return nil
		},
	}
	c := newTestClient(t, srv)

	sub, err := c.Subscribe(t.Context(), []string{"kernel.*"}, func(*kernelv1.BusEvent) {})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	select {
	case <-streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the fake server's Subscribe to start")
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
