package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/pluggableharness/agent/internal/telemetry"
)

func TestRecordDynamicMetric_counter(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	err := p.RecordDynamicMetric(context.Background(), "plugin.tool.github.calls", telemetry.DynamicMetricKindCounter, 3, map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("RecordDynamicMetric: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	sum := findFloatSum(t, rm, "plugin.tool.github.calls")
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 3 {
		t.Fatalf("data points = %+v, want one point of 3", sum.DataPoints)
	}
}

func TestRecordDynamicMetric_kindMismatch(t *testing.T) {
	t.Parallel()
	p, _ := newTestProvider(t)
	ctx := context.Background()

	if err := p.RecordDynamicMetric(ctx, "plugin.tool.github.x", telemetry.DynamicMetricKindCounter, 1, nil); err != nil {
		t.Fatalf("first RecordDynamicMetric: %v", err)
	}
	err := p.RecordDynamicMetric(ctx, "plugin.tool.github.x", telemetry.DynamicMetricKindHistogram, 1, nil)
	if !errors.Is(err, telemetry.ErrDynamicMetricKindMismatch) {
		t.Fatalf("second RecordDynamicMetric (different kind) = %v, want ErrDynamicMetricKindMismatch", err)
	}
}

func TestRecordDynamicMetric_attributesDroppedOverBound(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)
	ctx := context.Background()

	attrs := make(map[string]string, telemetry.MaxDynamicMetricAttributes+2)
	for i := range telemetry.MaxDynamicMetricAttributes + 2 {
		attrs[string(rune('a'+i))] = "v"
	}
	if err := p.RecordDynamicMetric(ctx, "plugin.tool.github.attrs", telemetry.DynamicMetricKindCounter, 1, attrs); err != nil {
		t.Fatalf("RecordDynamicMetric: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	dropped := findSum(t, rm, "pluggableharness.telemetry.record_metrics.attributes_dropped")
	if len(dropped.DataPoints) != 1 || dropped.DataPoints[0].Value != 2 {
		t.Fatalf("dropped-attributes data points = %+v, want one point of 2", dropped.DataPoints)
	}
}
