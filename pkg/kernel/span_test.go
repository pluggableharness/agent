package kernel_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/pluggableharness/agent/pkg/kernel"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

// spanCapture records every ExportSpansRequest a fakeServer's ExportSpans
// method receives.
type spanCapture struct {
	mu   sync.Mutex
	reqs []*kernelv1.ExportSpansRequest
}

func (c *spanCapture) record(req *kernelv1.ExportSpansRequest) (*kernelv1.ExportSpansResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, req)
	return &kernelv1.ExportSpansResult{}, nil
}

func (c *spanCapture) requests() []*kernelv1.ExportSpansRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*kernelv1.ExportSpansRequest, len(c.reqs))
	copy(out, c.reqs)
	return out
}

// realSpan builds one genuine sdktrace.ReadOnlySpan by running it through
// a real in-memory TracerProvider — the most faithful way to exercise
// convertReadOnlySpan against actual SDK-produced identity/timing values,
// rather than a hand-built fake that might not match the SDK's real
// field shapes.
func realSpan(t *testing.T, name string, attrs ...attribute.KeyValue) sdktrace.ReadOnlySpan {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Millisecond)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	tracer := tp.Tracer("test-scope", oteltrace.WithInstrumentationVersion("1.0.0"))
	_, span := tracer.Start(context.Background(), name, oteltrace.WithAttributes(attrs...))
	span.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	return spans[0].Snapshot()
}

func TestSpanExporter_exportSpans(t *testing.T) {
	t.Parallel()

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter()

	span := realSpan(t, "tool.execute", attribute.String("tool.name", "github"))

	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{span}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	reqs := capture.requests()
	if len(reqs) != 1 || len(reqs[0].GetSpans()) != 1 {
		t.Fatalf("requests = %+v, want one request with one span", reqs)
	}

	got := reqs[0].GetSpans()[0]
	if got.GetName() != "tool.execute" {
		t.Errorf("Name = %q, want tool.execute", got.GetName())
	}
	if len(got.GetTraceId()) != 32 {
		t.Errorf("TraceId = %q, want 32 lowercase hex chars", got.GetTraceId())
	}
	if len(got.GetSpanId()) != 16 {
		t.Errorf("SpanId = %q, want 16 lowercase hex chars", got.GetSpanId())
	}
	if got.GetScope().GetName() != "test-scope" || got.GetScope().GetVersion() != "1.0.0" {
		t.Errorf("Scope = %+v, want test-scope/1.0.0", got.GetScope())
	}
	if got.GetAttributes().GetFields()["tool.name"].GetStringValue() != "github" {
		t.Errorf("attributes[tool.name] = %v, want github", got.GetAttributes())
	}
}

func TestSpanExporter_emptyIsNoop(t *testing.T) {
	t.Parallel()

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter()

	if err := exporter.ExportSpans(t.Context(), nil); err != nil {
		t.Fatalf("ExportSpans(nil): %v", err)
	}
	if got := capture.requests(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none", got)
	}
}

func TestSpanExporter_sessionID(t *testing.T) {
	t.Parallel()

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter(kernel.WithExportSessionID("sess-1"))

	span := realSpan(t, "x")
	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{span}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	reqs := capture.requests()
	if len(reqs) != 1 || reqs[0].GetSessionId() != "sess-1" {
		t.Fatalf("requests = %+v, want one request with session_id=sess-1", reqs)
	}
}

func TestSpanExporter_shutdownIsNoop(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, &fakeServer{})
	exporter := c.NewSpanExporter()
	if err := exporter.Shutdown(t.Context()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestSpanExporter_rootSpanHasNilParent(t *testing.T) {
	t.Parallel()

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter()

	span := realSpan(t, "root")
	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{span}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	got := capture.requests()[0].GetSpans()[0]
	if got.ParentSpanId != nil {
		t.Errorf("ParentSpanId = %v, want nil for a root span", got.ParentSpanId)
	}
}

func TestSpanExporter_eventsLinksAndSliceAttributes(t *testing.T) {
	t.Parallel()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Millisecond)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tracer := tp.Tracer("test-scope")

	_, linked := tracer.Start(context.Background(), "linked")
	linked.End()

	_, span := tracer.Start(context.Background(), "with-event-and-link",
		oteltrace.WithLinks(oteltrace.LinkFromContext(oteltrace.ContextWithSpan(context.Background(), linked),
			attribute.String("link.reason", "related"))),
		oteltrace.WithAttributes(
			attribute.StringSlice("tags", []string{"a", "b"}),
			attribute.Int64Slice("counts", []int64{1, 2}),
			attribute.Float64Slice("ratios", []float64{0.5, 1.5}),
			attribute.BoolSlice("flags", []bool{true, false}),
		),
	)
	span.AddEvent("checkpoint", oteltrace.WithAttributes(attribute.String("stage", "start")))
	span.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	var target sdktrace.ReadOnlySpan
	for _, s := range exp.GetSpans() {
		if s.Name == "with-event-and-link" {
			target = s.Snapshot()
		}
	}
	if target == nil {
		t.Fatal("span not found")
	}

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter()
	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{target}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	got := capture.requests()[0].GetSpans()[0]
	if len(got.GetEvents()) != 1 || got.GetEvents()[0].GetName() != "checkpoint" {
		t.Errorf("Events = %+v, want one event named checkpoint", got.GetEvents())
	}
	if got.GetEvents()[0].GetAttributes().GetFields()["stage"].GetStringValue() != "start" {
		t.Errorf("event attributes = %v, want stage=start", got.GetEvents()[0].GetAttributes())
	}
	if len(got.GetLinks()) != 1 || len(got.GetLinks()[0].GetSpanId()) != 16 {
		t.Errorf("Links = %+v, want one link with a 16-char span id", got.GetLinks())
	}
	if got.GetLinks()[0].GetAttributes().GetFields()["link.reason"].GetStringValue() != "related" {
		t.Errorf("link attributes = %v, want link.reason=related", got.GetLinks()[0].GetAttributes())
	}

	attrs := got.GetAttributes().GetFields()
	if tags := attrs["tags"].GetListValue().GetValues(); len(tags) != 2 || tags[0].GetStringValue() != "a" {
		t.Errorf("tags = %v, want [a, b]", attrs["tags"])
	}
	if counts := attrs["counts"].GetListValue().GetValues(); len(counts) != 2 || counts[0].GetNumberValue() != 1 {
		t.Errorf("counts = %v, want [1, 2]", attrs["counts"])
	}
	if ratios := attrs["ratios"].GetListValue().GetValues(); len(ratios) != 2 {
		t.Errorf("ratios = %v, want 2 elements", attrs["ratios"])
	}
	if flags := attrs["flags"].GetListValue().GetValues(); len(flags) != 2 || !flags[0].GetBoolValue() {
		t.Errorf("flags = %v, want [true, false]", attrs["flags"])
	}
}

func TestSpanExporter_errorStatus(t *testing.T) {
	t.Parallel()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Millisecond)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tracer := tp.Tracer("test-scope")

	_, span := tracer.Start(context.Background(), "failing")
	span.SetStatus(otelcodes.Error, "boom")
	span.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter()
	if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{exp.GetSpans()[0].Snapshot()}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	got := capture.requests()[0].GetSpans()[0]
	if got.GetStatus().GetCode() != tracev1.StatusCode_STATUS_CODE_ERROR {
		t.Errorf("Status.Code = %v, want STATUS_CODE_ERROR", got.GetStatus().GetCode())
	}
	if got.GetStatus().GetMessage() != "boom" {
		t.Errorf("Status.Message = %q, want boom", got.GetStatus().GetMessage())
	}
}

func TestSpanExporter_childSpanHasParent(t *testing.T) {
	t.Parallel()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Millisecond)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tracer := tp.Tracer("test-scope")

	ctx, parent := tracer.Start(context.Background(), "parent")
	_, child := tracer.Start(ctx, "child")
	child.End()
	parent.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}

	capture := &spanCapture{}
	c := newTestClient(t, &fakeServer{exportSpansFunc: capture.record})
	exporter := c.NewSpanExporter()

	var readOnly []sdktrace.ReadOnlySpan
	for _, s := range spans {
		readOnly = append(readOnly, s.Snapshot())
	}
	if err := exporter.ExportSpans(t.Context(), readOnly); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	got := capture.requests()[0].GetSpans()
	var childWire *tracev1.Span
	for _, s := range got {
		if s.GetName() == "child" {
			childWire = s
		}
	}
	if childWire == nil {
		t.Fatal("child span not found in relayed batch")
	}
	if childWire.ParentSpanId == nil || len(childWire.GetParentSpanId()) != 16 {
		t.Errorf("child.ParentSpanId = %v, want a 16-char hex parent id", childWire.ParentSpanId)
	}
}
