package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// newTestProvider returns a Provider wired to a fresh fake backend, plus
// a spans func that force-flushes and returns everything recorded so far.
func newTestProvider(t *testing.T) (*telemetry.Provider, *fake.Backend) {
	t.Helper()
	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"
	backend := fake.New()
	p, err := telemetry.New(context.Background(), cfg, backend, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return p, backend
}

func flushedSpans(t *testing.T, p *telemetry.Provider, backend *fake.Backend) tracetest.SpanStubs {
	t.Helper()
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	return backend.Spans.GetSpans()
}

func findAttr(t *testing.T, attrs []attribute.KeyValue, key attribute.Key) attribute.Value {
	t.Helper()
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value
		}
	}
	t.Fatalf("attribute %s not found in %v", key, attrs)
	return attribute.Value{}
}

func TestStartSession(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartSession(context.Background(), telemetry.SessionSpan{
		SessionID:       "sess-1",
		ParentSessionID: "sess-0",
		RootSessionID:   "sess-0",
		Depth:           1,
		AgentProfile:    "default",
	})
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Status.Code != codes.Ok {
		t.Errorf("Status = %v, want Ok", got.Status.Code)
	}
	if findAttr(t, got.Attributes, telemetry.SessionIDKey).AsString() != "sess-1" {
		t.Errorf("session.id = %q, want sess-1", findAttr(t, got.Attributes, telemetry.SessionIDKey).AsString())
	}
	if findAttr(t, got.Attributes, telemetry.SessionDepthKey).AsInt64() != 1 {
		t.Errorf("session.depth = %d, want 1", findAttr(t, got.Attributes, telemetry.SessionDepthKey).AsInt64())
	}
}

func TestStartTurn(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartTurn(context.Background(), 3)
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	if got := findAttr(t, spans[0].Attributes, telemetry.TurnIndexKey).AsInt64(); got != 3 {
		t.Errorf("turn.index = %d, want 3", got)
	}
}

func TestHookDispatch_subscriberNests(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	dispatchCtx, dispatchSpan := p.StartHookDispatch(context.Background(), telemetry.HookPointPreToolCall)
	_, subSpan := p.StartHookSubscriber(dispatchCtx, telemetry.SubscriberModeObserve, nil)
	telemetry.EndSpan(subSpan, nil)
	telemetry.EndSpan(dispatchSpan, nil)

	spans := flushedSpans(t, p, backend)
	if len(spans) != 2 {
		t.Fatalf("len(spans) = %d, want 2", len(spans))
	}

	var dispatch, sub tracetest.SpanStub
	for _, s := range spans {
		switch s.Name {
		case "hook.dispatch":
			dispatch = s
		case "hook.subscriber":
			sub = s
		}
	}
	if dispatch.Name == "" || sub.Name == "" {
		t.Fatalf("expected both hook.dispatch and hook.subscriber spans, got %+v", spans)
	}
	if sub.Parent.SpanID() != dispatch.SpanContext.SpanID() {
		t.Errorf("hook.subscriber's parent span ID = %s, want %s (hook.dispatch's span ID)",
			sub.Parent.SpanID(), dispatch.SpanContext.SpanID())
	}
	if findAttr(t, dispatch.Attributes, telemetry.HookPointKey).AsString() != telemetry.HookPointPreToolCall {
		t.Errorf("hook.point = %q, want %q", findAttr(t, dispatch.Attributes, telemetry.HookPointKey).AsString(), telemetry.HookPointPreToolCall)
	}
	if findAttr(t, sub.Attributes, telemetry.SubscriberModeKey).AsString() != telemetry.SubscriberModeObserve {
		t.Errorf("subscriber.mode = %q, want observe", findAttr(t, sub.Attributes, telemetry.SubscriberModeKey).AsString())
	}
}

func TestStartModelCall_withProducer(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	producer := &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_MODEL, Name: "anthropic", Version: "1.0.0"}
	_, span := p.StartModelCall(context.Background(), "claude-sonnet", producer)
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "model.call" {
		t.Errorf("Name = %q, want model.call", got.Name)
	}
	if findAttr(t, got.Attributes, telemetry.ModelIDKey).AsString() != "claude-sonnet" {
		t.Errorf("model.id = %q, want claude-sonnet", findAttr(t, got.Attributes, telemetry.ModelIDKey).AsString())
	}
	if findAttr(t, got.Attributes, telemetry.ProducerNameKey).AsString() != "anthropic" {
		t.Errorf("producer.name = %q, want anthropic", findAttr(t, got.Attributes, telemetry.ProducerNameKey).AsString())
	}
}

func TestStartToolExecute(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartToolExecute(context.Background(), "read_file", telemetry.ToolKindResource, nil)
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if findAttr(t, got.Attributes, telemetry.ToolNameKey).AsString() != "read_file" {
		t.Errorf("tool.name mismatch")
	}
	if findAttr(t, got.Attributes, telemetry.ToolKindKey).AsString() != telemetry.ToolKindResource {
		t.Errorf("tool.kind mismatch")
	}
}

func TestStartPolicyEvaluate(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartPolicyEvaluate(context.Background())
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	if spans[0].Name != "policy.evaluate" {
		t.Errorf("Name = %q, want policy.evaluate", spans[0].Name)
	}
}

func TestStartRunSessionSpawn(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartRunSessionSpawn(context.Background(), "child-sess")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	if findAttr(t, spans[0].Attributes, telemetry.SessionIDKey).AsString() != "child-sess" {
		t.Errorf("session.id mismatch")
	}
}

func TestStartConfigLoad(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartConfigLoad(context.Background(), "/etc/pluggableharness-agent/agent.hcl")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "config.load" {
		t.Errorf("Name = %q, want config.load", got.Name)
	}
	if findAttr(t, got.Attributes, telemetry.FilePathKey).AsString() != "/etc/pluggableharness-agent/agent.hcl" {
		t.Errorf("file.path = %q, want /etc/pluggableharness-agent/agent.hcl", findAttr(t, got.Attributes, telemetry.FilePathKey).AsString())
	}
}

func TestStartGlobalConfigLoad(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartGlobalConfigLoad(context.Background(), "/home/user/.config/pluggableharness-agent/config.hcl")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "registry.global_config.load" {
		t.Errorf("Name = %q, want registry.global_config.load", got.Name)
	}
	if findAttr(t, got.Attributes, telemetry.FilePathKey).AsString() != "/home/user/.config/pluggableharness-agent/config.hcl" {
		t.Errorf("file.path mismatch")
	}
}

func TestStartLockFileLoad(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartLockFileLoad(context.Background(), "/workspace/.pluggableharness.agent.lock.hcl")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "registry.lockfile.load" {
		t.Errorf("Name = %q, want registry.lockfile.load", got.Name)
	}
	if findAttr(t, got.Attributes, telemetry.FilePathKey).AsString() != "/workspace/.pluggableharness.agent.lock.hcl" {
		t.Errorf("file.path mismatch")
	}
}

func TestStartChecksumVerify(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartChecksumVerify(context.Background(), "/plugins/anthropic/anthropic_linux_amd64", "linux_amd64")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "registry.checksum.verify" {
		t.Errorf("Name = %q, want registry.checksum.verify", got.Name)
	}
	if findAttr(t, got.Attributes, telemetry.FilePathKey).AsString() != "/plugins/anthropic/anthropic_linux_amd64" {
		t.Errorf("file.path mismatch")
	}
	if findAttr(t, got.Attributes, telemetry.PlatformKey).AsString() != "linux_amd64" {
		t.Errorf("platform = %q, want linux_amd64", findAttr(t, got.Attributes, telemetry.PlatformKey).AsString())
	}
}

func TestStartPluginLaunch(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartPluginLaunch(context.Background(), "provider", "anthropic", "1.0.0")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "plugin.launch" {
		t.Errorf("Name = %q, want plugin.launch", got.Name)
	}
	if got.SpanKind != trace.SpanKindClient {
		t.Errorf("SpanKind = %v, want SpanKindClient", got.SpanKind)
	}
	if findAttr(t, got.Attributes, telemetry.ProducerCategoryKey).AsString() != "provider" {
		t.Errorf("producer.category = %q, want provider", findAttr(t, got.Attributes, telemetry.ProducerCategoryKey).AsString())
	}
	if findAttr(t, got.Attributes, telemetry.ProducerNameKey).AsString() != "anthropic" {
		t.Errorf("producer.name = %q, want anthropic", findAttr(t, got.Attributes, telemetry.ProducerNameKey).AsString())
	}
	if findAttr(t, got.Attributes, telemetry.ProducerVersionKey).AsString() != "1.0.0" {
		t.Errorf("producer.version = %q, want 1.0.0", findAttr(t, got.Attributes, telemetry.ProducerVersionKey).AsString())
	}
}

func TestStartEventBusPublish(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartEventBusPublish(context.Background(), "tool.result")
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Name != "eventbus.publish" {
		t.Errorf("Name = %q, want eventbus.publish", got.Name)
	}
	if findAttr(t, got.Attributes, telemetry.EventBusTopicKey).AsString() != "tool.result" {
		t.Errorf("eventbus.topic = %q, want tool.result", findAttr(t, got.Attributes, telemetry.EventBusTopicKey).AsString())
	}
}

func TestEndSpan_recordsError(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	_, span := p.StartToolExecute(context.Background(), "broken_tool", telemetry.ToolKindDataSource, nil)
	telemetry.EndSpan(span, errors.New("boom"))

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if got.Status.Code != codes.Error {
		t.Errorf("Status = %v, want Error", got.Status.Code)
	}
	if len(got.Events) == 0 {
		t.Error("EndSpan with a non-nil error should record an exception event")
	}
}
