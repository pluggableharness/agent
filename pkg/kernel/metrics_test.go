package kernel_test

import (
	"errors"
	"testing"
	"time"

	metricv1 "github.com/pluggableharness/agent/pkg/metric/proto/v1"

	"github.com/pluggableharness/agent/pkg/kernel"
)

func TestMetrics_countAndHistogram(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{}
	client := newTestClient(t, srv)

	if err := client.Metrics().Count(t.Context(), "requests", 3, map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("Count: %v", err)
	}
	if err := client.Metrics().Histogram(t.Context(), "latency", 12.5, "ms", nil); err != nil {
		t.Fatalf("Histogram: %v", err)
	}

	got := srv.recordMetricsRequests()
	if len(got) != 2 {
		t.Fatalf("got %d RecordMetrics calls, want 2", len(got))
	}

	counter := got[0].GetMetrics()[0]
	if counter.GetName() != "requests" {
		t.Errorf("name = %q, want requests", counter.GetName())
	}
	if counter.GetKind() != metricv1.MetricKind_METRIC_KIND_COUNTER {
		t.Errorf("kind = %v, want COUNTER", counter.GetKind())
	}
	if counter.GetIntValue() != 3 {
		t.Errorf("int value = %d, want 3", counter.GetIntValue())
	}
	if counter.GetAttributes()["status"] != "ok" {
		t.Errorf("attributes = %v, want status=ok", counter.GetAttributes())
	}
	if counter.GetTime() == nil {
		t.Error("time is unset; it is a MUST on the wire")
	}

	hist := got[1].GetMetrics()[0]
	if hist.GetKind() != metricv1.MetricKind_METRIC_KIND_HISTOGRAM {
		t.Errorf("kind = %v, want HISTOGRAM", hist.GetKind())
	}
	if hist.GetDoubleValue() != 12.5 {
		t.Errorf("double value = %v, want 12.5", hist.GetDoubleValue())
	}
	if hist.GetUnit() != "ms" {
		t.Errorf("unit = %q, want ms", hist.GetUnit())
	}
}

func TestMetrics_recordBatchSendsOneCall(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{}
	client := newTestClient(t, srv)

	// The reason RecordBatch exists: each call is a round trip, and a
	// plugin reporting per-request metrics should not pay one per metric.
	err := client.Metrics().RecordBatch(t.Context(),
		kernel.Observation{Name: "a", Kind: metricv1.MetricKind_METRIC_KIND_COUNTER, Int: 1},
		kernel.Observation{Name: "b", Kind: metricv1.MetricKind_METRIC_KIND_COUNTER, Int: 2},
	)
	if err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}

	got := srv.recordMetricsRequests()
	if len(got) != 1 {
		t.Fatalf("got %d calls, want 1 — the batch was not sent together", len(got))
	}
	if n := len(got[0].GetMetrics()); n != 2 {
		t.Errorf("got %d records in the batch, want 2", n)
	}
}

func TestMetrics_emptyBatchIsANoOp(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{}
	client := newTestClient(t, srv)

	// RecordMetricsRequest requires a non-empty batch, so sending one
	// would be a guaranteed rejection for a caller whose loop simply found
	// nothing to report.
	if err := client.Metrics().RecordBatch(t.Context()); err != nil {
		t.Fatalf("an empty batch returned %v, want nil", err)
	}
	if n := len(srv.recordMetricsRequests()); n != 0 {
		t.Errorf("an empty batch produced %d calls, want 0", n)
	}
}

func TestMetrics_rejectsMalformedObservationsLocally(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{}
	client := newTestClient(t, srv)

	tests := map[string]struct {
		obs     kernel.Observation
		wantErr error
	}{
		"no name": {
			obs:     kernel.Observation{Kind: metricv1.MetricKind_METRIC_KIND_COUNTER, Int: 1},
			wantErr: kernel.ErrMetricNameEmpty,
		},
		"no kind": {
			obs:     kernel.Observation{Name: "a", Int: 1},
			wantErr: kernel.ErrMetricKindUnset,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := client.Metrics().Record(t.Context(), tt.obs)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want wrapping %v", err, tt.wantErr)
			}
		})
	}

	// Rejected locally, so nothing reached the kernel: the caller learns
	// which observation was wrong instead of getting a batch-level
	// failure back.
	if n := len(srv.recordMetricsRequests()); n != 0 {
		t.Errorf("a malformed observation produced %d calls, want 0", n)
	}
}

func TestMetrics_sessionIDIsAttachedWhenSet(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{}
	client := newTestClient(t, srv)

	m := client.Metrics(kernel.WithMetricsSessionID("session-01ARZ3"))
	if err := m.Count(t.Context(), "requests", 1, nil); err != nil {
		t.Fatalf("Count: %v", err)
	}
	got := srv.recordMetricsRequests()
	if len(got) != 1 || got[0].GetSessionId() != "session-01ARZ3" {
		t.Errorf("session_id = %q, want session-01ARZ3", got[0].GetSessionId())
	}

	// Absent by default: it is optional on the wire, and a recorder with
	// no session has none to claim.
	if err := client.Metrics().Count(t.Context(), "requests", 1, nil); err != nil {
		t.Fatalf("Count: %v", err)
	}
	got = srv.recordMetricsRequests()
	if got[1].SessionId != nil {
		t.Errorf("session_id = %q, want absent", got[1].GetSessionId())
	}
}

func TestMetrics_explicitTimeIsPreserved(t *testing.T) {
	t.Parallel()

	srv := &fakeServer{}
	client := newTestClient(t, srv)

	at := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	err := client.Metrics().Record(t.Context(), kernel.Observation{
		Name: "a",
		Kind: metricv1.MetricKind_METRIC_KIND_UP_DOWN_COUNTER,
		Int:  1,
		Time: at,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got := srv.recordMetricsRequests()[0].GetMetrics()[0]
	if !got.GetTime().AsTime().Equal(at) {
		t.Errorf("time = %v, want %v", got.GetTime().AsTime(), at)
	}
}
