package otlphttp_test

import (
	"context"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/otlphttp"
)

// These tests only exercise construction — an HTTP exporter never dials
// until an actual export call, so no real network call happens here.
func TestBackend(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.Endpoint = "localhost:4318"
	cfg.Insecure = true

	b := otlphttp.New(cfg)
	if got := b.Name(); got != "otlphttp" {
		t.Errorf("Name() = %q, want otlphttp", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp, err := b.TraceExporter(ctx)
	if err != nil {
		t.Fatalf("TraceExporter: %v", err)
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
	if err := logExp.Shutdown(ctx); err != nil {
		t.Errorf("log exporter Shutdown: %v", err)
	}

	uploader, err := b.TraceUploader(ctx)
	if err != nil {
		t.Fatalf("TraceUploader: %v", err)
	}
	if err := uploader.Stop(ctx); err != nil {
		t.Errorf("uploader Stop: %v", err)
	}
}
