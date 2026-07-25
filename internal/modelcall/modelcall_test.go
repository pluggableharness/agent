package modelcall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	eventv1 "github.com/pluggableharness/agent/pkg/event/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/cost"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/retrypolicy"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// --- fakes ---

// fakeStream is a hand-written grpc.ServerStreamingClient[modelv1.StreamEvent]
// (go-testing.md: fakes, not mocking frameworks) that replays a scripted
// slice of events, then returns either a scripted terminal error or io.EOF.
type fakeStream struct {
	ctx     context.Context
	events  []*modelv1.StreamEvent
	recvErr error // returned once events are exhausted; nil means io.EOF
	idx     int
}

func (s *fakeStream) Recv() (*modelv1.StreamEvent, error) {
	if s.idx < len(s.events) {
		ev := s.events[s.idx]
		s.idx++
		return ev, nil
	}
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	return nil, io.EOF
}

func (s *fakeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeStream) Trailer() metadata.MD         { return nil }
func (s *fakeStream) CloseSend() error             { return nil }
func (s *fakeStream) Context() context.Context     { return s.ctx }
func (s *fakeStream) SendMsg(any) error            { return nil }
func (s *fakeStream) RecvMsg(any) error            { return nil }

// streamScript is one StreamCompletion call's scripted outcome.
type streamScript struct {
	dialErr error // returned by StreamCompletion itself, before any Recv
	events  []*modelv1.StreamEvent
	recvErr error // returned after events are exhausted, instead of io.EOF
}

// fakeModelServiceClient is a hand-written modelv1.ModelServiceClient
// (go-testing.md) scripting one outcome per successive StreamCompletion
// call — attempt N of a Complete call consumes scripts[N-1]. Every other
// RPC is unused by this package and panics if called, the intended
// "un-overridden method" signal go-testing.md describes.
type fakeModelServiceClient struct {
	scripts []streamScript
	calls   int
}

func (f *fakeModelServiceClient) GetCapabilities(context.Context, *modelv1.GetCapabilitiesRequest, ...grpc.CallOption) (*modelv1.GetCapabilitiesResponse, error) {
	panic("fakeModelServiceClient: GetCapabilities not scripted for this test")
}

func (f *fakeModelServiceClient) Configure(context.Context, *modelv1.ConfigureRequest, ...grpc.CallOption) (*modelv1.ConfigureResponse, error) {
	panic("fakeModelServiceClient: Configure not scripted for this test")
}

func (f *fakeModelServiceClient) StreamCompletion(ctx context.Context, _ *modelv1.StreamCompletionRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[modelv1.StreamEvent], error) {
	idx := f.calls
	f.calls++
	if idx >= len(f.scripts) {
		panic("fakeModelServiceClient: StreamCompletion called more times than scripted")
	}
	sc := f.scripts[idx]
	if sc.dialErr != nil {
		return nil, sc.dialErr
	}
	return &fakeStream{ctx: ctx, events: sc.events, recvErr: sc.recvErr}, nil
}

func (f *fakeModelServiceClient) CountTokens(context.Context, *modelv1.CountTokensRequest, ...grpc.CallOption) (*modelv1.CountTokensResponse, error) {
	panic("fakeModelServiceClient: CountTokens not scripted for this test")
}

func (f *fakeModelServiceClient) Render(context.Context, *modelv1.RenderRequest, ...grpc.CallOption) (*modelv1.RenderResponse, error) {
	panic("fakeModelServiceClient: Render not scripted for this test")
}

func (f *fakeModelServiceClient) Describe(context.Context, *modelv1.DescribeRequest, ...grpc.CallOption) (*modelv1.DescribeResponse, error) {
	panic("fakeModelServiceClient: Describe not scripted for this test")
}

// fakeSink is a hand-written MessageSink recording every AppendMessage
// call for assertion.
type fakeSink struct {
	mu    sync.Mutex
	calls []sinkCall
	err   error
}

type sinkCall struct {
	ev   statebackend.Event
	cost statebackend.CostEntry
}

func (f *fakeSink) AppendMessage(_ context.Context, ev statebackend.Event, cost statebackend.CostEntry) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.calls = append(f.calls, sinkCall{ev: ev, cost: cost})
	return int64(len(f.calls)), nil
}

// fakeSleeper is a hand-written Config.Sleep recording every call's
// duration. onSleep, if set, runs before returning — the cancellation
// tests use it to cancel the context precisely during a backoff sleep.
type fakeSleeper struct {
	mu      sync.Mutex
	calls   []time.Duration
	onSleep func()
}

func (f *fakeSleeper) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.calls = append(f.calls, d)
	f.mu.Unlock()
	if f.onSleep != nil {
		f.onSleep()
	}
	return ctx.Err()
}

func (f *fakeSleeper) durations() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]time.Duration, len(f.calls))
	copy(out, f.calls)
	return out
}

// --- fixtures ---

// fixedJitter returns a Config.Jitter that always reports j, for
// deterministic backoff assertions.
func fixedJitter(j float64) func() float64 {
	return func() float64 { return j }
}

// testPricing is a single, unbounded-in-both-dimensions PricingTier —
// the degenerate single-tier case cost.ValidatePricing documents —
// cheap, exact round numbers so expected cost is easy to hand-verify.
func testPricing() *modelv1.Pricing {
	return &modelv1.Pricing{
		Currency: "USD",
		Tiers: []*modelv1.PricingTier{
			{InputPerMtok: 2_000_000, OutputPerMtok: 10_000_000},
		},
	}
}

func testProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_MODEL,
		Name:     "acme",
		Version:  "1.2.3",
	}
}

func testModelHandle(client modelv1.ModelServiceClient) providercatalog.ModelHandle {
	return providercatalog.ModelHandle{
		Ref:      agentprofile.ModelRef{Provider: "acme", ID: "acme-large"},
		Producer: testProducer(),
		Spec:     &modelv1.ModelSpec{Id: "acme-large", Pricing: testPricing()},
		Client:   client,
	}
}

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func testTelemetry(t *testing.T) *telemetry.Provider {
	t.Helper()
	prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })
	return prov
}

func testSettings(maxRetries, sessionMax int) retrypolicy.Settings {
	return retrypolicy.Settings{
		BaseDelay:         10 * time.Millisecond,
		BackoffFactor:     2,
		MaxRetries:        maxRetries,
		SessionMaxRetries: sessionMax,
	}
}

// --- event builders ---

func textDeltaEvent(text string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_TextDelta_{TextDelta: &modelv1.StreamEvent_TextDelta{Text: text}}}
}

func usageEvent(u *modelv1.Usage) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Usage{Usage: u}}
}

func stopEvent(reason modelv1.StopReason) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Stop_{Stop: &modelv1.StreamEvent_Stop{Reason: reason}}}
}

func errorEvent(modelErr *modelv1.ModelError) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Error_{Error: &modelv1.StreamEvent_Error{Error: modelErr}}}
}

func successScript(text string, input, output int64) streamScript {
	return streamScript{events: []*modelv1.StreamEvent{
		textDeltaEvent(text),
		usageEvent(&modelv1.Usage{InputTokens: input, OutputTokens: output}),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN),
	}}
}

func errorScript(category modelv1.ModelErrorCategory, retryAfter *durationpb.Duration) streamScript {
	return streamScript{events: []*modelv1.StreamEvent{
		errorEvent(&modelv1.ModelError{Category: category, Message: "boom", RetryAfter: retryAfter}),
	}}
}

// --- tests ---

func TestComplete_Success(t *testing.T) {
	t.Parallel()

	client := &fakeModelServiceClient{scripts: []streamScript{successScript("hello", 100, 50)}}
	sink := &fakeSink{}
	var logBuf bytes.Buffer
	caller := New(Config{
		Retry:     testSettings(3, 10),
		Events:    sink,
		Jitter:    fixedJitter(0),
		Clock:     func() time.Time { return time.Unix(1000, 0).UTC() },
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&logBuf),
	})

	req := Request{
		Model:     testModelHandle(client),
		MessageID: "msg-1",
		Request:   &modelv1.StreamCompletionRequest{ModelId: "acme-large"},
	}

	resp, err := caller.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", resp.Attempts)
	}
	if resp.Stop != modelv1.StopReason_STOP_REASON_END_TURN {
		t.Errorf("Stop = %v, want STOP_REASON_END_TURN", resp.Stop)
	}
	if resp.Message.GetId() != "msg-1" {
		t.Errorf("Message.Id = %q, want msg-1", resp.Message.GetId())
	}
	if got := resp.Message.GetProducedByModelId(); got != "acme-large" {
		t.Errorf("Message.ProducedByModelId = %q, want acme-large", got)
	}
	if got := resp.Message.GetProducedByProvider(); got != "acme" {
		t.Errorf("Message.ProducedByProvider = %q, want acme", got)
	}

	wantCost := cost.Compute(testPricing().Tiers[0], &modelv1.Usage{InputTokens: 100, OutputTokens: 50})
	if resp.CostUSD != wantCost {
		t.Errorf("CostUSD = %v, want %v", resp.CostUSD, wantCost)
	}

	if len(sink.calls) != 1 {
		t.Fatalf("AppendMessage calls = %d, want 1", len(sink.calls))
	}
	call := sink.calls[0]
	if call.ev.ID != "msg-1" {
		t.Errorf("persisted event ID = %q, want msg-1", call.ev.ID)
	}
	if call.ev.Kind != kernelv1.EventKind_EVENT_KIND_MESSAGE {
		t.Errorf("persisted event Kind = %v, want EVENT_KIND_MESSAGE", call.ev.Kind)
	}
	if call.ev.SchemaVersion != "1" {
		t.Errorf("persisted event SchemaVersion = %q, want 1", call.ev.SchemaVersion)
	}
	if call.cost.CostUSD != wantCost {
		t.Errorf("persisted CostEntry.CostUSD = %v, want %v", call.cost.CostUSD, wantCost)
	}
	if call.cost.InputTokens != 100 || call.cost.OutputTokens != 50 {
		t.Errorf("persisted CostEntry tokens = (%d, %d), want (100, 50)", call.cost.InputTokens, call.cost.OutputTokens)
	}

	var payload eventv1.MessageEvent
	if err := proto.Unmarshal(call.ev.Payload, &payload); err != nil {
		t.Fatalf("unmarshal persisted payload: %v", err)
	}
	if payload.GetCostUsd() != wantCost {
		t.Errorf("payload.CostUsd = %v, want %v", payload.GetCostUsd(), wantCost)
	}
	if payload.GetMessage().GetId() != "msg-1" {
		t.Errorf("payload.Message.Id = %q, want msg-1", payload.GetMessage().GetId())
	}
}

func TestComplete_RateLimited_RetriesThenGivesUp(t *testing.T) {
	t.Parallel()

	client := &fakeModelServiceClient{scripts: []streamScript{
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
	}}
	sleeper := &fakeSleeper{}
	caller := New(Config{
		Retry:     testSettings(2, 10), // MaxRetries=2 -> 3 total attempts before giving up
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     sleeper.sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	_, err := caller.Complete(context.Background(), req)

	var classified *Error
	if !errors.As(err, &classified) {
		t.Fatalf("Complete error = %v (%T), want *Error", err, err)
	}
	if classified.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED {
		t.Errorf("Category = %v, want RATE_LIMITED", classified.Category)
	}
	if classified.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", classified.Attempts)
	}
	if client.calls != 3 {
		t.Errorf("StreamCompletion calls = %d, want 3", client.calls)
	}
	if got := len(sleeper.durations()); got != 2 {
		t.Errorf("Sleep calls = %d, want 2", got)
	}
}

func TestComplete_RetryAfterHonoredVerbatim(t *testing.T) {
	t.Parallel()

	wantDelay := 37 * time.Millisecond
	client := &fakeModelServiceClient{scripts: []streamScript{
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, durationpb.New(wantDelay)),
		successScript("ok", 10, 10),
	}}
	sleeper := &fakeSleeper{}
	caller := New(Config{
		Retry:     testSettings(3, 10),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0.999), // must be ignored: retry_after overrides backoff+jitter entirely
		Sleep:     sleeper.sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	resp, err := caller.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", resp.Attempts)
	}

	durations := sleeper.durations()
	if len(durations) != 1 {
		t.Fatalf("Sleep calls = %d, want 1", len(durations))
	}
	if durations[0] != wantDelay {
		t.Errorf("Sleep called with %v, want exactly %v (retry_after verbatim, not computed backoff)", durations[0], wantDelay)
	}
}

func TestComplete_NonRetryableReactions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category modelv1.ModelErrorCategory
	}{
		{"context_length_exceeded", modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED},
		{"auth_error", modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR},
		{"invalid_request", modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST},
		{"content_filtered", modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeModelServiceClient{scripts: []streamScript{errorScript(tt.category, nil)}}
			sleeper := &fakeSleeper{}
			caller := New(Config{
				Retry:     testSettings(5, 10),
				Events:    &fakeSink{},
				Jitter:    fixedJitter(0),
				Sleep:     sleeper.sleep,
				Telemetry: testTelemetry(t),
				Logger:    testLogger(&bytes.Buffer{}),
			})

			req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
			_, err := caller.Complete(context.Background(), req)

			var classified *Error
			if !errors.As(err, &classified) {
				t.Fatalf("Complete error = %v (%T), want *Error", err, err)
			}
			if classified.Category != tt.category {
				t.Errorf("Category = %v, want %v", classified.Category, tt.category)
			}
			if classified.Attempts != 1 {
				t.Errorf("Attempts = %d, want 1 (zero retries)", classified.Attempts)
			}
			if client.calls != 1 {
				t.Errorf("StreamCompletion calls = %d, want 1 — no fallback-model logic exists in this package", client.calls)
			}
			if len(sleeper.durations()) != 0 {
				t.Errorf("Sleep was called %d times, want 0", len(sleeper.durations()))
			}
		})
	}
}

func TestSessionRetriesRemaining_sharedAcrossCompleteCalls(t *testing.T) {
	t.Parallel()

	// SessionMaxRetries=3, per-attempt MaxRetries=10 (so only the
	// session cap is ever the binding constraint here). The first call
	// fails rate_limited twice, retries twice (spending 2 of the 3
	// session-wide retries), then succeeds on its 3rd attempt — the
	// per-attempt cap never binds since MaxRetries=10. Only 1 session
	// retry remains for the second call: its first attempt fails
	// rate_limited, retries once (spending the last session retry), its
	// second attempt fails rate_limited again, and this time
	// SessionRetriesRemaining is 0 so it gives up instead of retrying a
	// 3rd time — even though the per-attempt cap alone would still allow
	// it.
	client := &fakeModelServiceClient{scripts: []streamScript{
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
		successScript("first", 1, 1),
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
	}}
	caller := New(Config{
		Retry:     testSettings(10, 3),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := func() Request {
		return Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	}

	if _, err := caller.Complete(context.Background(), req()); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if got := caller.SessionRetriesRemaining(); got != 1 {
		t.Fatalf("SessionRetriesRemaining after first call = %d, want 1 (3 - 2 spent)", got)
	}

	// The second call's first attempt fails rate_limited; only 1 session
	// retry remains, so it must retry exactly once more and then give up
	// — even though per-attempt MaxRetries=10 would otherwise allow more.
	_, err := caller.Complete(context.Background(), req())
	var classified *Error
	if !errors.As(err, &classified) {
		t.Fatalf("second Complete error = %v (%T), want *Error", err, err)
	}
	if classified.Attempts != 2 {
		t.Errorf("second call Attempts = %d, want 2 (session cap, not per-attempt cap, binds)", classified.Attempts)
	}
	if got := caller.SessionRetriesRemaining(); got != 0 {
		t.Errorf("SessionRetriesRemaining after second call = %d, want 0", got)
	}
}

func TestComplete_CancellationMidStream(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancelingStream := &cancelingFakeStream{ctx: ctx, cancel: cancel}
	client := &cancelingClient{stream: cancelingStream}

	var logBuf bytes.Buffer
	caller := New(Config{
		Retry:     testSettings(3, 10),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&logBuf),
	})

	req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	_, err := caller.Complete(ctx, req)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete error = %v, want context.Canceled", err)
	}
	var classified *Error
	if errors.As(err, &classified) {
		t.Fatalf("Complete error wrapped cancellation in *Error: %v", classified)
	}
	if strings.Contains(logBuf.String(), "level=ERROR") {
		t.Errorf("cancellation was logged as ERROR:\n%s", logBuf.String())
	}
}

// cancelingFakeStream cancels its own context on the first Recv call and
// reports the resulting context.Canceled — simulating the kernel closing
// the stream (a user interrupt/timeout/turn abort) while a Recv is
// in-flight, per .claude/rules/grpc.md's cancellation-is-normal-control-flow
// rule.
type cancelingFakeStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	called bool
}

func (s *cancelingFakeStream) Recv() (*modelv1.StreamEvent, error) {
	if !s.called {
		s.called = true
		s.cancel()
	}
	return nil, s.ctx.Err()
}

func (s *cancelingFakeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *cancelingFakeStream) Trailer() metadata.MD         { return nil }
func (s *cancelingFakeStream) CloseSend() error             { return nil }
func (s *cancelingFakeStream) Context() context.Context     { return s.ctx }
func (s *cancelingFakeStream) SendMsg(any) error            { return nil }
func (s *cancelingFakeStream) RecvMsg(any) error            { return nil }

// cancelingClient is a minimal modelv1.ModelServiceClient whose
// StreamCompletion always returns the given pre-built stream.
type cancelingClient struct {
	stream *cancelingFakeStream
}

func (c *cancelingClient) GetCapabilities(context.Context, *modelv1.GetCapabilitiesRequest, ...grpc.CallOption) (*modelv1.GetCapabilitiesResponse, error) {
	panic("not scripted")
}
func (c *cancelingClient) Configure(context.Context, *modelv1.ConfigureRequest, ...grpc.CallOption) (*modelv1.ConfigureResponse, error) {
	panic("not scripted")
}
func (c *cancelingClient) StreamCompletion(context.Context, *modelv1.StreamCompletionRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[modelv1.StreamEvent], error) {
	return c.stream, nil
}
func (c *cancelingClient) CountTokens(context.Context, *modelv1.CountTokensRequest, ...grpc.CallOption) (*modelv1.CountTokensResponse, error) {
	panic("not scripted")
}
func (c *cancelingClient) Render(context.Context, *modelv1.RenderRequest, ...grpc.CallOption) (*modelv1.RenderResponse, error) {
	panic("not scripted")
}
func (c *cancelingClient) Describe(context.Context, *modelv1.DescribeRequest, ...grpc.CallOption) (*modelv1.DescribeResponse, error) {
	panic("not scripted")
}

func TestComplete_CancellationMidBackoffSleep(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeModelServiceClient{scripts: []streamScript{
		errorScript(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, nil),
	}}
	sleeper := &fakeSleeper{onSleep: cancel}

	var logBuf bytes.Buffer
	caller := New(Config{
		Retry:     testSettings(5, 10),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     sleeper.sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&logBuf),
	})

	req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	_, err := caller.Complete(ctx, req)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete error = %v, want context.Canceled", err)
	}
	var classified *Error
	if errors.As(err, &classified) {
		t.Fatalf("Complete error wrapped cancellation in *Error: %v", classified)
	}
	if strings.Contains(logBuf.String(), "level=ERROR") {
		t.Errorf("cancellation during backoff was logged as ERROR:\n%s", logBuf.String())
	}
}

func TestComplete_RetriedAttemptStartsFreshAccumulator(t *testing.T) {
	t.Parallel()

	client := &fakeModelServiceClient{scripts: []streamScript{
		{
			// First attempt: some text streams, then the transport
			// itself fails (no structured ModelError ever arrives) —
			// exercises the fallback classification path too.
			events:  []*modelv1.StreamEvent{textDeltaEvent("partial-from-first-attempt")},
			recvErr: status.Error(codes.Unavailable, "connection reset"),
		},
		successScript("final", 5, 5),
	}}
	caller := New(Config{
		Retry:     testSettings(3, 10),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	resp, err := caller.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", resp.Attempts)
	}

	if len(resp.Message.GetContent()) != 1 {
		t.Fatalf("Message.Content has %d blocks, want 1 (no leakage from the failed first attempt)", len(resp.Message.GetContent()))
	}
	text := resp.Message.GetContent()[0].GetText().GetText()
	if text != "final" {
		t.Errorf("Message text = %q, want exactly \"final\" (partial-from-first-attempt must not leak in)", text)
	}
}

func TestComplete_TransportFallbackClassification_ResourceExhausted(t *testing.T) {
	t.Parallel()

	// A badly-behaved transport failure that never carried a structured
	// ModelError: exercises classifyTransportErr's fallback path
	// end-to-end via a genuine grpc status error.
	client := &fakeModelServiceClient{scripts: []streamScript{
		{dialErr: status.Error(codes.ResourceExhausted, "quota exceeded")},
		successScript("ok", 1, 1),
	}}
	caller := New(Config{
		Retry:     testSettings(3, 10),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := Request{Model: testModelHandle(client), MessageID: "msg-1", Request: &modelv1.StreamCompletionRequest{}}
	resp, err := caller.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 (ResourceExhausted classified as retryable RATE_LIMITED)", resp.Attempts)
	}
}

// TestMessageSink_realStatebackendSession proves *statebackend.Session
// satisfies MessageSink for real, against an actual sqlite-backed session
// over t.TempDir() — go-testing.md's "local sqlite with no subprocess
// stays inside the unit tier" reasoning, already applied identically in
// internal/sessionstate's own tests.
func TestMessageSink_realStatebackendSession(t *testing.T) {
	t.Parallel()

	st, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessionID := statebackend.NewSessionID(time.Now())
	sess, err := st.Create(context.Background(), statebackend.SessionMeta{
		SessionID: sessionID,
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	var sink MessageSink = sess

	ev := statebackend.Event{
		ID:            statebackend.NewEventID(time.Now()),
		Timestamp:     time.Now(),
		Kind:          kernelv1.EventKind_EVENT_KIND_MESSAGE,
		Producer:      testProducer(),
		SchemaVersion: "1",
		Payload:       []byte("{}"),
	}
	entry := statebackend.CostEntry{ProviderName: "acme", ModelID: "acme-large", InputTokens: 1, OutputTokens: 1, CostUSD: 0.001}
	if _, err := sink.AppendMessage(context.Background(), ev, entry); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
}

// --- whitebox unit tests for the package's smaller helpers ---

func TestError_ErrorAndUnwrap(t *testing.T) {
	t.Parallel()

	underlying := errors.New("boom")
	e := &Error{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, Attempts: 1, Err: underlying}

	if !errors.Is(e, underlying) {
		t.Errorf("errors.Is(e, underlying) = false, want true (Unwrap must expose Err)")
	}
	if got := e.Error(); !strings.Contains(got, "boom") || !strings.Contains(got, "attempts=1") {
		t.Errorf("Error() = %q, want it to mention the underlying message and attempts", got)
	}
}

func TestDefaultJitter(t *testing.T) {
	t.Parallel()

	for range 10 {
		j := defaultJitter()
		if j < 0 || j >= 1 {
			t.Fatalf("defaultJitter() = %v, want in [0, 1)", j)
		}
	}
}

func TestDefaultSleep(t *testing.T) {
	t.Parallel()

	t.Run("zero duration returns immediately", func(t *testing.T) {
		t.Parallel()
		if err := defaultSleep(context.Background(), 0); err != nil {
			t.Errorf("defaultSleep(0) = %v, want nil", err)
		}
	})

	t.Run("elapses normally", func(t *testing.T) {
		t.Parallel()
		if err := defaultSleep(context.Background(), time.Millisecond); err != nil {
			t.Errorf("defaultSleep = %v, want nil", err)
		}
	})

	t.Run("canceled context returns ctx.Err()", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := defaultSleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Errorf("defaultSleep on canceled ctx = %v, want context.Canceled", err)
		}
	})
}

func TestNew_defaults(t *testing.T) {
	t.Parallel()

	caller := New(Config{
		Retry:     testSettings(1, 1),
		Events:    &fakeSink{},
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})
	if caller.cfg.Jitter == nil {
		t.Error("Jitter default not applied")
	} else if j := caller.cfg.Jitter(); j < 0 || j >= 1 {
		t.Errorf("default Jitter() = %v, want in [0, 1)", j)
	}
	if caller.cfg.Clock == nil {
		t.Error("Clock default not applied")
	} else if caller.cfg.Clock().IsZero() {
		t.Error("default Clock() returned the zero time")
	}
	if caller.cfg.Sleep == nil {
		t.Error("Sleep default not applied")
	} else if err := caller.cfg.Sleep(context.Background(), 0); err != nil {
		t.Errorf("default Sleep(0) = %v, want nil", err)
	}
}

func TestSessionRetriesRemaining_neverNegative(t *testing.T) {
	t.Parallel()

	caller := New(Config{Retry: testSettings(1, 1), Events: &fakeSink{}, Telemetry: testTelemetry(t), Logger: testLogger(&bytes.Buffer{})})
	caller.sessionRetriesUsed.Store(5) // more than SessionMaxRetries=1
	if got := caller.SessionRetriesRemaining(); got != 0 {
		t.Errorf("SessionRetriesRemaining() = %d, want 0 (never negative)", got)
	}
}

func TestModelErrToErr(t *testing.T) {
	t.Parallel()

	t.Run("message only", func(t *testing.T) {
		t.Parallel()
		err := modelErrToErr(&modelv1.ModelError{Message: "plain"})
		if err.Error() != "plain" {
			t.Errorf("modelErrToErr = %q, want %q", err.Error(), "plain")
		}
	})

	t.Run("message plus raw_detail", func(t *testing.T) {
		t.Parallel()
		raw := "vendor-code-429"
		err := modelErrToErr(&modelv1.ModelError{Message: "rate limited", RawDetail: &raw})
		got := err.Error()
		if !strings.Contains(got, "rate limited") || !strings.Contains(got, raw) {
			t.Errorf("modelErrToErr = %q, want it to contain both the message and raw_detail", got)
		}
	})
}

func TestIsCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context.Canceled", context.Canceled, true},
		{"wrapped context.Canceled", errors.New("wrap: " + context.Canceled.Error()), false},
		{"grpc codes.Canceled status", status.Error(codes.Canceled, "canceled"), true},
		{"unrelated error", errors.New("boom"), false},
		{"grpc codes.Unavailable status", status.Error(codes.Unavailable, "down"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCancellation(tt.err); got != tt.want {
				t.Errorf("isCancellation(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyTransportErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want modelv1.ModelErrorCategory
	}{
		{"ResourceExhausted", status.Error(codes.ResourceExhausted, "quota"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED},
		{"Unavailable", status.Error(codes.Unavailable, "down"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED},
		{"Unauthenticated", status.Error(codes.Unauthenticated, "no creds"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR},
		{"InvalidArgument", status.Error(codes.InvalidArgument, "bad"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST},
		{"FailedPrecondition", status.Error(codes.FailedPrecondition, "filtered"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED},
		{"Internal maps to UNKNOWN", status.Error(codes.Internal, "oops"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN},
		{"non-status error maps to UNKNOWN", errors.New("plain error, no grpc status"), modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyTransportErr(tt.err)
			if got.GetCategory() != tt.want {
				t.Errorf("classifyTransportErr(%v).Category = %v, want %v", tt.err, got.GetCategory(), tt.want)
			}
			if got.GetMessage() == "" {
				t.Errorf("classifyTransportErr(%v).Message is empty", tt.err)
			}
		})
	}
}

func TestDoAttempt_streamEndsWithoutTerminalEvent(t *testing.T) {
	t.Parallel()

	// A stream that sends a text delta then simply closes (io.EOF)
	// without ever sending a stop or error event — structurally invalid,
	// per streamaccum's Result() ok=false contract.
	client := &fakeModelServiceClient{scripts: []streamScript{
		{events: []*modelv1.StreamEvent{textDeltaEvent("hi")}},
	}}
	caller := New(Config{
		Retry:     testSettings(0, 0),
		Events:    &fakeSink{},
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	_, _, _, modelErr, err := caller.doAttempt(context.Background(), Request{Model: testModelHandle(client), MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}, 1)
	if err == nil {
		t.Fatal("doAttempt returned nil err, want a structural error")
	}
	if modelErr != nil {
		t.Errorf("modelErr = %v, want nil (this is an unclassified structural failure)", modelErr)
	}
}

func TestDoAttempt_observeErrorIsUnclassified(t *testing.T) {
	t.Parallel()

	// A thinking_signature event with no thinking block open is a
	// structurally invalid sequence per streamaccum's Observe contract —
	// exercises doAttempt's acc.Observe error branch.
	badEvent := &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ThinkingSignature_{ThinkingSignature: &modelv1.StreamEvent_ThinkingSignature{Signature: []byte("sig")}}}
	client := &fakeModelServiceClient{scripts: []streamScript{{events: []*modelv1.StreamEvent{badEvent}}}}
	caller := New(Config{
		Retry:     testSettings(0, 0),
		Events:    &fakeSink{},
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	_, _, _, modelErr, err := caller.doAttempt(context.Background(), Request{Model: testModelHandle(client), MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}, 1)
	if err == nil {
		t.Fatal("doAttempt returned nil err, want the wrapped streamaccum error")
	}
	if modelErr != nil {
		t.Errorf("modelErr = %v, want nil", modelErr)
	}
}

func TestPersist_resolveTierError(t *testing.T) {
	t.Parallel()

	client := &fakeModelServiceClient{}
	caller := New(Config{
		Retry:     testSettings(0, 0),
		Events:    &fakeSink{},
		Clock:     func() time.Time { return time.Unix(0, 0).UTC() },
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	handle := testModelHandle(client)
	handle.Spec = &modelv1.ModelSpec{Id: "acme-large", Pricing: &modelv1.Pricing{Currency: "USD"}} // no tiers, not free -> ResolveTier fails
	req := Request{Model: handle, MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}

	msg := &contentv1.Message{Role: contentv1.Role_ROLE_ASSISTANT}
	if _, err := caller.persist(context.Background(), req, msg, &modelv1.Usage{InputTokens: 1}); err == nil {
		t.Fatal("persist returned nil error, want a pricing-tier resolution failure")
	}
}

// TestPersist_freePricingBillsZeroWithoutATier covers the shape
// cost.ValidatePricing accepts with no tier coverage at all: free = true,
// tiers = []. Resolving a tier for it would fail every completion from a
// legally-declared free provider.
func TestPersist_freePricingBillsZeroWithoutATier(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	caller := New(Config{
		Retry:     testSettings(0, 0),
		Events:    sink,
		Clock:     func() time.Time { return time.Unix(0, 0).UTC() },
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	handle := testModelHandle(&fakeModelServiceClient{})
	handle.Spec = &modelv1.ModelSpec{
		Id:      "acme-free",
		Pricing: &modelv1.Pricing{Currency: "USD", Free: true},
	}
	req := Request{Model: handle, MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}

	msg := &contentv1.Message{Role: contentv1.Role_ROLE_ASSISTANT}
	costUSD, err := caller.persist(context.Background(), req, msg, &modelv1.Usage{InputTokens: 12, OutputTokens: 6})
	if err != nil {
		t.Fatalf("persist for a free model: %v", err)
	}
	if costUSD != 0 {
		t.Errorf("cost = %v, want 0 for a free model", costUSD)
	}
}

func TestPersist_appendMessageError(t *testing.T) {
	t.Parallel()

	sinkErr := errors.New("disk full")
	caller := New(Config{
		Retry:     testSettings(0, 0),
		Events:    &fakeSink{err: sinkErr},
		Clock:     func() time.Time { return time.Unix(0, 0).UTC() },
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := Request{Model: testModelHandle(&fakeModelServiceClient{}), MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}
	msg := &contentv1.Message{Role: contentv1.Role_ROLE_ASSISTANT}
	if _, err := caller.persist(context.Background(), req, msg, &modelv1.Usage{InputTokens: 1, OutputTokens: 1}); !errors.Is(err, sinkErr) {
		t.Errorf("persist error = %v, want it to wrap %v", err, sinkErr)
	}
}

func TestComplete_UnexpectedInternalErrorIsUnwrapped(t *testing.T) {
	t.Parallel()

	// A stream that closes without ever sending a stop/error event is a
	// structural failure (streamaccum's Result() ok=false), not a
	// classified model error — exercises Complete's non-cancellation
	// attemptErr branch (logged at ERROR, returned bare, never wrapped
	// in *Error).
	client := &fakeModelServiceClient{scripts: []streamScript{
		{events: []*modelv1.StreamEvent{textDeltaEvent("hi")}},
	}}
	var logBuf bytes.Buffer
	caller := New(Config{
		Retry:     testSettings(3, 3),
		Events:    &fakeSink{},
		Jitter:    fixedJitter(0),
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&logBuf),
	})

	req := Request{Model: testModelHandle(client), MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}
	_, err := caller.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("Complete returned nil error, want the structural failure")
	}
	var classified *Error
	if errors.As(err, &classified) {
		t.Fatalf("Complete wrapped an internal structural failure in *Error: %v", classified)
	}
	if !strings.Contains(logBuf.String(), "level=ERROR") {
		t.Errorf("expected an ERROR-level log for the internal failure, got:\n%s", logBuf.String())
	}
}

func TestComplete_PersistFailurePropagates(t *testing.T) {
	t.Parallel()

	sinkErr := errors.New("disk full")
	client := &fakeModelServiceClient{scripts: []streamScript{successScript("hi", 1, 1)}}
	caller := New(Config{
		Retry:     testSettings(3, 3),
		Events:    &fakeSink{err: sinkErr},
		Jitter:    fixedJitter(0),
		Sleep:     (&fakeSleeper{}).sleep,
		Telemetry: testTelemetry(t),
		Logger:    testLogger(&bytes.Buffer{}),
	})

	req := Request{Model: testModelHandle(client), MessageID: "m", Request: &modelv1.StreamCompletionRequest{}}
	if _, err := caller.Complete(context.Background(), req); !errors.Is(err, sinkErr) {
		t.Errorf("Complete error = %v, want it to wrap %v", err, sinkErr)
	}
}
