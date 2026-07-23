package noop_test

import (
	"context"
	"testing"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

func TestBackend(t *testing.T) {
	t.Parallel()

	b := noop.New()
	if got := b.Name(); got != "noop" {
		t.Errorf("Name() = %q, want noop", got)
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
		t.Errorf("exporter Shutdown: %v", err)
	}

	reader, err := b.MetricReader(ctx)
	if err != nil {
		t.Fatalf("MetricReader: %v", err)
	}
	if reader == nil {
		t.Fatal("MetricReader returned nil with a nil error")
	}

	logExp, err := b.LogExporter(ctx)
	if err != nil {
		t.Fatalf("LogExporter: %v", err)
	}
	if err := logExp.Export(ctx, nil); err != nil {
		t.Errorf("Export: %v", err)
	}
	if err := logExp.ForceFlush(ctx); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
	if err := logExp.Shutdown(ctx); err != nil {
		t.Errorf("log exporter Shutdown: %v", err)
	}
}

// TestBackend_endToEnd wires the noop driver through telemetry.New and
// exercises a full span lifecycle, confirming nothing panics or errors
// when everything is discarded at the export boundary.
func TestBackend_endToEnd(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"

	p, err := telemetry.New(context.Background(), cfg, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}

	_, span := p.StartTurn(context.Background(), 0)
	telemetry.EndSpan(span, nil)

	if err := p.ForceFlush(context.Background()); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}
