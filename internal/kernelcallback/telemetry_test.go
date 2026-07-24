package kernelcallback

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metricv1 "github.com/pluggableharness/agent/pkg/metric/proto/v1"
	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

func testProducer() *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "github",
		Version:  "1.0.0",
	}
}

func testSpan(t *testing.T) *tracev1.Span {
	t.Helper()
	return &tracev1.Span{
		TraceId:   "0123456789abcdef0123456789abcdef",
		SpanId:    "fedcba9876543210",
		Name:      "tool.execute",
		Kind:      tracev1.SpanKind_SPAN_KIND_INTERNAL,
		StartTime: timestamppb.Now(),
		EndTime:   timestamppb.New(time.Now().Add(time.Second)),
		Status:    &tracev1.Status{Code: tracev1.StatusCode_STATUS_CODE_OK},
		Scope:     &tracev1.InstrumentationScope{Name: "plugin.tool.github"},
	}
}

func TestServer_ExportSpans(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.ExportSpans(t.Context(), &kernelv1.ExportSpansRequest{
		Spans: []*tracev1.Span{testSpan(t)},
	})
	if err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	got := f.relayClient.ResourceSpans()
	if len(got) != 1 {
		t.Fatalf("relayed ResourceSpans = %d, want 1", len(got))
	}
}

func TestServer_ExportSpans_emptyRejected(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.ExportSpans(t.Context(), &kernelv1.ExportSpansRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestServer_RecordMetrics_counter(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	req := &kernelv1.RecordMetricsRequest{
		Metrics: []*metricv1.MetricRecord{
			{
				Name:  "calls",
				Kind:  metricv1.MetricKind_METRIC_KIND_COUNTER,
				Value: &metricv1.MetricRecord_IntValue{IntValue: 3},
				Time:  timestamppb.Now(),
			},
		},
	}
	if _, err := f.server.RecordMetrics(t.Context(), req); err != nil {
		t.Fatalf("RecordMetrics: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := f.telemetry.Metrics.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "plugin.tool.github.calls" {
				found = true
			}
		}
	}
	if !found {
		t.Error("plugin.tool.github.calls not found in collected metrics")
	}
}

func TestServer_RecordMetrics_emptyRejected(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	_, err := f.server.RecordMetrics(t.Context(), &kernelv1.RecordMetricsRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestServer_RecordMetrics_unspecifiedKindRejected(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	req := &kernelv1.RecordMetricsRequest{
		Metrics: []*metricv1.MetricRecord{
			{Name: "x", Kind: metricv1.MetricKind_METRIC_KIND_UNSPECIFIED, Time: timestamppb.Now()},
		},
	}
	_, err := f.server.RecordMetrics(t.Context(), req)
	assertCode(t, err, codes.InvalidArgument)
}

func TestServer_RecordMetrics_kindMismatchRejected(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	first := &kernelv1.RecordMetricsRequest{
		Metrics: []*metricv1.MetricRecord{
			{Name: "x", Kind: metricv1.MetricKind_METRIC_KIND_COUNTER, Value: &metricv1.MetricRecord_IntValue{IntValue: 1}, Time: timestamppb.Now()},
		},
	}
	if _, err := f.server.RecordMetrics(t.Context(), first); err != nil {
		t.Fatalf("first RecordMetrics: %v", err)
	}

	second := &kernelv1.RecordMetricsRequest{
		Metrics: []*metricv1.MetricRecord{
			{Name: "x", Kind: metricv1.MetricKind_METRIC_KIND_HISTOGRAM, Value: &metricv1.MetricRecord_DoubleValue{DoubleValue: 1}, Time: timestamppb.Now()},
		},
	}
	_, err := f.server.RecordMetrics(t.Context(), second)
	assertCode(t, err, codes.InvalidArgument)
}

func TestServer_GetTelemetryConfig(t *testing.T) {
	t.Parallel()
	f := newTestServer(t, testProducer())

	got, err := f.server.GetTelemetryConfig(t.Context(), &kernelv1.GetTelemetryConfigRequest{})
	if err != nil {
		t.Fatalf("GetTelemetryConfig: %v", err)
	}
	if !got.TracesEnabled || !got.MetricsEnabled || !got.LogsEnabled {
		t.Errorf("GetTelemetryConfig = %+v, want all three signals enabled (telemetry.DefaultConfig)", got)
	}
	if got.SamplingRatio != 1.0 {
		t.Errorf("SamplingRatio = %v, want 1.0", got.SamplingRatio)
	}
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %v error, got nil", want)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status error", err)
	}
	if st.Code() != want {
		t.Fatalf("status code = %v, want %v", st.Code(), want)
	}
}
