package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/pluggableharness/agent/internal/telemetry"
)

func TestRecordUsage_metrics(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	ctx, span := p.StartModelCall(context.Background(), "claude-sonnet", nil)
	p.RecordUsage(ctx, span, telemetry.Usage{
		InputTokens:      100,
		OutputTokens:     50,
		CacheReadTokens:  10,
		CacheWriteTokens: 5,
		CostUSD:          0.0123,
		ModelID:          "claude-sonnet",
	})
	telemetry.EndSpan(span, nil)

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	tokens := findSum(t, rm, "pluggableharness.agent.tokens")
	if len(tokens.DataPoints) != 4 {
		t.Fatalf("pluggableharness.agent.tokens data points = %d, want 4 (input/output/cache_read/cache_write)", len(tokens.DataPoints))
	}
	var total int64
	for _, dp := range tokens.DataPoints {
		total += dp.Value
	}
	if total != 100+50+10+5 {
		t.Errorf("total tokens = %d, want 165", total)
	}

	costUSD := findFloatSum(t, rm, "pluggableharness.agent.cost.usd")
	if len(costUSD.DataPoints) != 1 || costUSD.DataPoints[0].Value != 0.0123 {
		t.Errorf("pluggableharness.agent.cost.usd data points = %+v, want one point of 0.0123", costUSD.DataPoints)
	}
}

func TestRecordUsage_zeroFieldsNotRecorded(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	ctx, span := p.StartModelCall(context.Background(), "claude-sonnet", nil)
	p.RecordUsage(ctx, span, telemetry.Usage{
		InputTokens: 100,
		// OutputTokens, CacheReadTokens, CacheWriteTokens, CostUSD all zero.
		ModelID: "claude-sonnet",
	})
	telemetry.EndSpan(span, nil)

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	tokens := findSum(t, rm, "pluggableharness.agent.tokens")
	if len(tokens.DataPoints) != 1 {
		t.Errorf("pluggableharness.agent.tokens data points = %d, want 1 (only input was non-zero)", len(tokens.DataPoints))
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "pluggableharness.agent.cost.usd" {
				t.Errorf("pluggableharness.agent.cost.usd recorded a data point despite CostUSD=0: %+v", m.Data)
			}
		}
	}
}

func TestRecordUsage_spanAttributes(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	ctx, span := p.StartModelCall(context.Background(), "claude-sonnet", nil)
	p.RecordUsage(ctx, span, telemetry.Usage{InputTokens: 100, OutputTokens: 50, ModelID: "claude-sonnet"})
	telemetry.EndSpan(span, nil)

	spans := flushedSpans(t, p, backend)
	got := spans[0]
	if findAttr(t, got.Attributes, "gen_ai.usage.input_tokens").AsInt64() != 100 {
		t.Errorf("gen_ai.usage.input_tokens mismatch")
	}
	if findAttr(t, got.Attributes, "gen_ai.usage.output_tokens").AsInt64() != 50 {
		t.Errorf("gen_ai.usage.output_tokens mismatch")
	}
}

func TestRecordUsage_nilSpan(t *testing.T) {
	t.Parallel()
	p, _ := newTestProvider(t)

	// Must not panic when span is nil (a caller that only cares about the
	// aggregate metrics, not per-call tracing detail).
	p.RecordUsage(context.Background(), nil, telemetry.Usage{InputTokens: 1, ModelID: "x"})
}

func findSum(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Sum[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("metric %s has unexpected data type %T", name, m.Data)
				}
				return sum
			}
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.Sum[int64]{}
}

func findFloatSum(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Sum[float64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				sum, ok := m.Data.(metricdata.Sum[float64])
				if !ok {
					t.Fatalf("metric %s has unexpected data type %T", name, m.Data)
				}
				return sum
			}
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.Sum[float64]{}
}
