package kernelcallback

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// fakeSubscribeStream is a hand-written fake of
// kernelv1.KernelCallbackService_SubscribeServer (go-testing.md: fakes,
// not mocking frameworks). sendFunc, when set, is called for every Send;
// otherwise Send records the event and returns nil.
type fakeSubscribeStream struct {
	ctx      context.Context
	sendFunc func(*kernelv1.BusEvent) error

	mu   sync.Mutex
	sent []*kernelv1.BusEvent
}

func newFakeSubscribeStream(ctx context.Context) *fakeSubscribeStream {
	return &fakeSubscribeStream{ctx: ctx}
}

func (f *fakeSubscribeStream) Send(ev *kernelv1.BusEvent) error {
	if f.sendFunc != nil {
		return f.sendFunc(ev)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ev)
	return nil
}

func (f *fakeSubscribeStream) Sent() []*kernelv1.BusEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*kernelv1.BusEvent, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeSubscribeStream) Context() context.Context     { return f.ctx }
func (f *fakeSubscribeStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeSubscribeStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeSubscribeStream) SetTrailer(metadata.MD)       {}
func (f *fakeSubscribeStream) SendMsg(any) error            { return nil }
func (f *fakeSubscribeStream) RecvMsg(any) error            { return nil }

// publishFileChanged publishes one "file_changed" event through
// f.server.Publish — the fixture used by every Subscribe test below.
func publishFileChanged(t *testing.T, f *testFixture) {
	t.Helper()
	if _, err := f.server.Publish(t.Context(), &kernelv1.PublishRequest{
		EventType: "file_changed", PayloadType: "text/plain", SchemaVersion: "1",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// waitUntil retries fn (publishing one event before each check) up to 200
// times, 10ms apart, until condition reports true — a bounded poll rather
// than a fixed sleep, for the inherent race between a goroutine-launched
// Subscribe call reaching internal/eventbus's registration point and this
// test's first Publish.
func waitUntil(t *testing.T, condition func() bool, publish func()) {
	t.Helper()
	for range 200 {
		publish()
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestServer_Publish_constructsTopic(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	result, err := f.server.Publish(t.Context(), &kernelv1.PublishRequest{
		EventType:     "file_changed",
		Payload:       []byte("data"),
		PayloadType:   "text/plain",
		SchemaVersion: "1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.GetTopic() != "plugin.tool.github.file_changed" {
		t.Errorf("Publish result topic = %q, want plugin.tool.github.file_changed", result.GetTopic())
	}
}

func TestServer_Publish_validation(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	tests := []struct {
		name string
		req  *kernelv1.PublishRequest
	}{
		{"empty event_type", &kernelv1.PublishRequest{PayloadType: "text/plain", SchemaVersion: "1"}},
		{"event_type contains dot", &kernelv1.PublishRequest{EventType: "a.b", PayloadType: "text/plain", SchemaVersion: "1"}},
		{"event_type contains wildcard", &kernelv1.PublishRequest{EventType: "a*", PayloadType: "text/plain", SchemaVersion: "1"}},
		{"empty payload_type", &kernelv1.PublishRequest{EventType: "x", SchemaVersion: "1"}},
		{"empty schema_version", &kernelv1.PublishRequest{EventType: "x", PayloadType: "text/plain"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.server.Publish(t.Context(), tt.req)
			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestServer_Subscribe_receivesPublishedEvent(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	ctx, cancel := context.WithCancel(t.Context())
	stream := newFakeSubscribeStream(ctx)

	done := make(chan error, 1)
	go func() {
		done <- f.server.Subscribe(&kernelv1.SubscribeRequest{TopicFilters: []string{"plugin.tool.github.*"}}, stream)
	}()

	waitUntil(t,
		func() bool { return len(stream.Sent()) > 0 },
		func() { publishFileChanged(t, f) },
	)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Subscribe returned an error after context cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not return after its context was canceled")
	}

	sent := stream.Sent()
	if len(sent) == 0 || sent[0].GetTopic() != "plugin.tool.github.file_changed" {
		t.Fatalf("Sent() = %+v, want at least one event on plugin.tool.github.file_changed", sent)
	}
}

func TestServer_Subscribe_validation(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())
	stream := newFakeSubscribeStream(t.Context())

	tests := []struct {
		name string
		req  *kernelv1.SubscribeRequest
	}{
		{"empty filters", &kernelv1.SubscribeRequest{}},
		{"mid-string wildcard", &kernelv1.SubscribeRequest{TopicFilters: []string{"plugin.*.github"}}},
		{"wildcard not preceded by dot", &kernelv1.SubscribeRequest{TopicFilters: []string{"plugintool*"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := f.server.Subscribe(tt.req, stream)
			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestServer_Subscribe_backpressureCloses(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer(), func(cfg *Config) { cfg.BusSubscribeQueueBound = 1 })

	release := make(chan struct{})
	sendStarted := make(chan struct{}, 1)
	stream := newFakeSubscribeStream(t.Context())
	stream.sendFunc = func(*kernelv1.BusEvent) error {
		select {
		case sendStarted <- struct{}{}:
		default:
		}
		<-release
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- f.server.Subscribe(&kernelv1.SubscribeRequest{TopicFilters: []string{"plugin.tool.github.*"}}, stream)
	}()

	waitUntil(t,
		func() bool {
			select {
			case <-sendStarted:
				return true
			default:
				return false
			}
		},
		func() { publishFileChanged(t, f) },
	)

	// The first Send is now blocked on release. With bound=1, the next
	// publish fills the bridge's bounded buffer and the one after that
	// overflows it — signalling the (buffered, non-blocking) overflow
	// channel, which the main select loop can only observe once its
	// current, still-blocked Send call returns.
	publishFileChanged(t, f)
	publishFileChanged(t, f)
	close(release)

	select {
	case err := <-done:
		assertCode(t, err, codes.ResourceExhausted)
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not return after exceeding its backpressure bound")
	}
}
