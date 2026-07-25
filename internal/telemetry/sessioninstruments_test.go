package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// TestSessionsStarted_records exercises Instruments.SessionsStarted the way
// a future RunSession implementation will: a bare Add(ctx, 1), with no
// attributes (a session start has no natural bounded dimension to
// break out by).
func TestSessionsStarted_records(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	p.Instruments().SessionsStarted.Add(context.Background(), 1)

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	sum := findSum(t, rm, "pluggableharness.sessions.started")
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Errorf("pluggableharness.sessions.started data points = %+v, want one point of 1", sum.DataPoints)
	}
}

// TestSessionsEnded_recordsByStatus exercises Instruments.SessionsEnded with
// the bounded session.status attribute a future session-end path will
// attach — SessionStatusKey's fixed 7-value vocabulary.
func TestSessionsEnded_recordsByStatus(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	ctx := context.Background()
	p.Instruments().SessionsEnded.Add(ctx, 1, metric.WithAttributes(telemetry.SessionStatusKey.String(telemetry.SessionStatusCompleted)))
	p.Instruments().SessionsEnded.Add(ctx, 1, metric.WithAttributes(telemetry.SessionStatusKey.String(telemetry.SessionStatusCancelled)))

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	sum := findSum(t, rm, "pluggableharness.sessions.ended")
	if len(sum.DataPoints) != 2 {
		t.Fatalf("pluggableharness.sessions.ended data points = %d, want 2 (completed, cancelled)", len(sum.DataPoints))
	}
	var sawCompleted, sawCancelled bool
	for _, dp := range sum.DataPoints {
		status, ok := dp.Attributes.Value(telemetry.SessionStatusKey)
		if !ok {
			t.Fatalf("data point missing session.status attribute: %+v", dp)
		}
		switch status.AsString() {
		case telemetry.SessionStatusCompleted:
			sawCompleted = true
		case telemetry.SessionStatusCancelled:
			sawCancelled = true
		default:
			t.Errorf("unexpected session.status = %q", status.AsString())
		}
	}
	if !sawCompleted || !sawCancelled {
		t.Errorf("expected both completed and cancelled data points, got %+v", sum.DataPoints)
	}
}

// TestTokenCountFallbacks_recordsByReason exercises
// Instruments.TokenCountFallbacks with TokenCountFallbackReasonKey's bounded
// reason vocabulary, and confirms no provider-name attribute is attached
// (TokenCountFallbackReasonKey's cardinality-discipline doc comment).
func TestTokenCountFallbacks_recordsByReason(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	ctx := context.Background()
	p.Instruments().TokenCountFallbacks.Add(ctx, 1, metric.WithAttributes(telemetry.TokenCountFallbackReasonKey.String(telemetry.FallbackReasonUnimplemented)))

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	sum := findSum(t, rm, "pluggableharness.token_count.fallbacks")
	if len(sum.DataPoints) != 1 {
		t.Fatalf("pluggableharness.token_count.fallbacks data points = %d, want 1", len(sum.DataPoints))
	}
	dp := sum.DataPoints[0]
	reason, ok := dp.Attributes.Value(telemetry.TokenCountFallbackReasonKey)
	if !ok || reason.AsString() != telemetry.FallbackReasonUnimplemented {
		t.Errorf("fallback_reason = %+v, want %q", reason, telemetry.FallbackReasonUnimplemented)
	}
	if _, ok := dp.Attributes.Value(telemetry.ProducerNameKey); ok {
		t.Errorf("pluggableharness.token_count.fallbacks must not carry a producer.name attribute")
	}
}
