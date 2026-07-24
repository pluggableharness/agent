package telemetryrelay_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

func testSpan(t *testing.T, name string) *tracev1.Span {
	t.Helper()
	return &tracev1.Span{
		TraceId:   "0123456789abcdef0123456789abcdef",
		SpanId:    "fedcba9876543210",
		Name:      name,
		Kind:      tracev1.SpanKind_SPAN_KIND_INTERNAL,
		StartTime: timestamppb.Now(),
		EndTime:   timestamppb.New(time.Now().Add(time.Second)),
		Status:    &tracev1.Status{Code: tracev1.StatusCode_STATUS_CODE_OK},
		Scope:     &tracev1.InstrumentationScope{Name: "plugin.tool.github", Version: "1.0.0"},
	}
}

func TestRelay_upload(t *testing.T) {
	t.Parallel()

	recorder := fake.NewRelayedSpansRecorder()
	relay := telemetryrelay.New(recorder)

	producer := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "github",
		Version:  "1.2.3",
	}
	spans := []*tracev1.Span{testSpan(t, "tool.execute")}

	if err := relay.Upload(context.Background(), spans, producer); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got := recorder.ResourceSpans()
	if len(got) != 1 {
		t.Fatalf("len(ResourceSpans()) = %d, want 1", len(got))
	}
	rs := got[0]
	if rs.Resource == nil {
		t.Fatal("Resource is nil")
	}

	attrs := make(map[string]string, len(rs.Resource.Attributes))
	for _, kv := range rs.Resource.Attributes {
		attrs[kv.Key] = kv.Value.GetStringValue()
	}
	if attrs["service.name"] != "github" {
		t.Errorf("service.name = %q, want github", attrs["service.name"])
	}
	if attrs["pluggableharness.producer.name"] != "github" {
		t.Errorf("pluggableharness.producer.name = %q, want github", attrs["pluggableharness.producer.name"])
	}
	if attrs["pluggableharness.producer.version"] != "1.2.3" {
		t.Errorf("pluggableharness.producer.version = %q, want 1.2.3", attrs["pluggableharness.producer.version"])
	}

	if len(rs.ScopeSpans) != 1 {
		t.Fatalf("len(ScopeSpans) = %d, want 1", len(rs.ScopeSpans))
	}
	if rs.ScopeSpans[0].Scope.Name != "plugin.tool.github" {
		t.Errorf("Scope.Name = %q, want plugin.tool.github", rs.ScopeSpans[0].Scope.Name)
	}
	if len(rs.ScopeSpans[0].Spans) != 1 || rs.ScopeSpans[0].Spans[0].Name != "tool.execute" {
		t.Fatalf("ScopeSpans[0].Spans = %+v, want one span named tool.execute", rs.ScopeSpans[0].Spans)
	}
}

func TestRelay_upload_groupsByScope(t *testing.T) {
	t.Parallel()

	recorder := fake.NewRelayedSpansRecorder()
	relay := telemetryrelay.New(recorder)

	a := testSpan(t, "a")
	a.Scope = &tracev1.InstrumentationScope{Name: "scope.a"}
	b := testSpan(t, "b")
	b.Scope = &tracev1.InstrumentationScope{Name: "scope.b"}
	a2 := testSpan(t, "a2")
	a2.Scope = &tracev1.InstrumentationScope{Name: "scope.a"}

	if err := relay.Upload(context.Background(), []*tracev1.Span{a, b, a2}, nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got := recorder.ResourceSpans()
	if len(got) != 1 {
		t.Fatalf("len(ResourceSpans()) = %d, want 1", len(got))
	}
	scopeSpans := got[0].ScopeSpans
	if len(scopeSpans) != 2 {
		t.Fatalf("len(ScopeSpans) = %d, want 2 (scope.a, scope.b)", len(scopeSpans))
	}
	if scopeSpans[0].Scope.Name != "scope.a" || len(scopeSpans[0].Spans) != 2 {
		t.Errorf("scope.a bucket = %+v, want 2 spans (a, a2) in first-seen order", scopeSpans[0])
	}
	if scopeSpans[1].Scope.Name != "scope.b" || len(scopeSpans[1].Spans) != 1 {
		t.Errorf("scope.b bucket = %+v, want 1 span (b)", scopeSpans[1])
	}
}

func TestRelay_upload_emptyIsNoop(t *testing.T) {
	t.Parallel()

	recorder := fake.NewRelayedSpansRecorder()
	relay := telemetryrelay.New(recorder)

	if err := relay.Upload(context.Background(), nil, nil); err != nil {
		t.Fatalf("Upload(nil spans): %v", err)
	}
	if got := recorder.ResourceSpans(); len(got) != 0 {
		t.Fatalf("ResourceSpans() = %v, want empty for a no-op Upload", got)
	}
}

func TestRelay_upload_nilProducerYieldsEmptyResource(t *testing.T) {
	t.Parallel()

	recorder := fake.NewRelayedSpansRecorder()
	relay := telemetryrelay.New(recorder)

	if err := relay.Upload(context.Background(), []*tracev1.Span{testSpan(t, "x")}, nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	got := recorder.ResourceSpans()
	if len(got) != 1 {
		t.Fatalf("len(ResourceSpans()) = %d, want 1", len(got))
	}
	if len(got[0].Resource.GetAttributes()) != 0 {
		t.Errorf("Resource.Attributes = %v, want empty for a nil producer", got[0].Resource.GetAttributes())
	}
}

func TestRelay_upload_invalidSpanErrors(t *testing.T) {
	t.Parallel()

	recorder := fake.NewRelayedSpansRecorder()
	relay := telemetryrelay.New(recorder)

	bad := testSpan(t, "bad")
	bad.TraceId = "not-hex"

	if err := relay.Upload(context.Background(), []*tracev1.Span{bad}, nil); err == nil {
		t.Fatal("Upload(invalid span) = nil error, want an error")
	}
	if got := recorder.ResourceSpans(); len(got) != 0 {
		t.Fatalf("ResourceSpans() = %v, want empty — a conversion failure must not partially upload", got)
	}
}

func TestRelay_stop(t *testing.T) {
	t.Parallel()

	recorder := fake.NewRelayedSpansRecorder()
	relay := telemetryrelay.New(recorder)

	if err := relay.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
