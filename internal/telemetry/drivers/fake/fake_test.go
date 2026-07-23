package fake_test

import (
	"context"
	"testing"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

func TestBackend(t *testing.T) {
	t.Parallel()

	b := fake.New()
	if got := b.Name(); got != "fake" {
		t.Errorf("Name() = %q, want fake", got)
	}

	ctx := context.Background()

	exp, err := b.TraceExporter(ctx)
	if err != nil {
		t.Fatalf("TraceExporter: %v", err)
	}
	if exp != b.Spans {
		t.Error("TraceExporter did not return b.Spans")
	}

	reader, err := b.MetricReader(ctx)
	if err != nil {
		t.Fatalf("MetricReader: %v", err)
	}
	if reader != b.Metrics {
		t.Error("MetricReader did not return b.Metrics")
	}

	logExp, err := b.LogExporter(ctx)
	if err != nil {
		t.Fatalf("LogExporter: %v", err)
	}
	if logExp != b.Logs {
		t.Error("LogExporter did not return b.Logs")
	}
}

func TestNew_freshRecorders(t *testing.T) {
	t.Parallel()

	b1 := fake.New()
	b2 := fake.New()

	if b1.Spans == b2.Spans {
		t.Error("New returned the same Spans recorder across two calls")
	}
	if b1.Metrics == b2.Metrics {
		t.Error("New returned the same Metrics reader across two calls")
	}
	if b1.Logs == b2.Logs {
		t.Error("New returned the same Logs recorder across two calls")
	}
}

func TestLogRecorder(t *testing.T) {
	t.Parallel()

	r := fake.NewLogRecorder()
	ctx := context.Background()

	if got := r.Records(); len(got) != 0 {
		t.Fatalf("Records() = %v, want empty", got)
	}

	rec := sdklogRecord("hello")
	if err := r.Export(ctx, []sdklog.Record{rec}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	got := r.Records()
	if len(got) != 1 {
		t.Fatalf("len(Records()) = %d, want 1", len(got))
	}
	if got[0].Body().AsString() != "hello" {
		t.Errorf("Body = %q, want hello", got[0].Body().AsString())
	}

	r.Reset()
	if got := r.Records(); len(got) != 0 {
		t.Fatalf("Records() after Reset = %v, want empty", got)
	}

	if err := r.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if err := r.ForceFlush(ctx); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
}

func sdklogRecord(body string) sdklog.Record {
	var rec sdklog.Record
	rec.SetBody(otellog.StringValue(body))
	return rec
}
