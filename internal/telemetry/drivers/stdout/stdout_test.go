package stdout_test

import (
	"context"
	"testing"

	"github.com/pluggableharness/agent/internal/telemetry/drivers/stdout"
)

func TestBackend(t *testing.T) {
	t.Parallel()

	b := stdout.New()
	if got := b.Name(); got != "stdout" {
		t.Errorf("Name() = %q, want stdout", got)
	}

	ctx := context.Background()

	exp, err := b.TraceExporter(ctx)
	if err != nil {
		t.Fatalf("TraceExporter: %v", err)
	}
	if err := exp.ExportSpans(ctx, nil); err != nil {
		t.Errorf("ExportSpans: %v", err)
	}
	if err := exp.Shutdown(ctx); err != nil {
		t.Errorf("trace exporter Shutdown: %v", err)
	}

	reader, err := b.MetricReader(ctx)
	if err != nil {
		t.Fatalf("MetricReader: %v", err)
	}
	if err := reader.Shutdown(ctx); err != nil {
		t.Errorf("metric reader Shutdown: %v", err)
	}

	logExp, err := b.LogExporter(ctx)
	if err != nil {
		t.Fatalf("LogExporter: %v", err)
	}
	if err := logExp.Export(ctx, nil); err != nil {
		t.Errorf("Export: %v", err)
	}
	if err := logExp.Shutdown(ctx); err != nil {
		t.Errorf("log exporter Shutdown: %v", err)
	}
}
