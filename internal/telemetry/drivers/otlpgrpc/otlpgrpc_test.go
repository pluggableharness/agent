package otlpgrpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/otlpgrpc"
)

// These tests only exercise construction. otlptracegrpc/otlpmetricgrpc dial
// lazily (grpc.NewClient, not grpc.Dial+WithBlock), so New never blocks or
// requires a reachable collector — no real network call happens here.
func TestBackend(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.Endpoint = "localhost:4317"
	cfg.Insecure = true

	b := otlpgrpc.New(cfg)
	if got := b.Name(); got != "otlpgrpc" {
		t.Errorf("Name() = %q, want otlpgrpc", got)
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
}
