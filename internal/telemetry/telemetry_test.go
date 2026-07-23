package telemetry_test

import (
	"context"
	"errors"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

func TestNew(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"

	p, err := telemetry.New(context.Background(), cfg, fake.New(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Tracer() == nil {
		t.Error("Tracer() = nil")
	}
	if p.Instruments() == nil {
		t.Error("Instruments() = nil")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNew_nilBackend(t *testing.T) {
	t.Parallel()

	_, err := telemetry.New(context.Background(), telemetry.DefaultConfig, nil, nil)
	if !errors.Is(err, telemetry.ErrNilBackend) {
		t.Fatalf("New: error = %v, want errors.Is ErrNilBackend", err)
	}
}

func TestNew_invalidConfigNeverTouchesBackend(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.SamplingRatio = -1 // invalid

	spy := &spyBackend{Backend: fake.New()}
	_, err := telemetry.New(context.Background(), cfg, spy, nil)
	if !errors.Is(err, telemetry.ErrInvalidConfig) {
		t.Fatalf("New: error = %v, want errors.Is ErrInvalidConfig", err)
	}
	if spy.traceCalls != 0 || spy.metricCalls != 0 || spy.logCalls != 0 {
		t.Errorf("New called the backend (%d trace, %d metric, %d log calls) despite an invalid config",
			spy.traceCalls, spy.metricCalls, spy.logCalls)
	}
}

func TestNew_disabledSignalsSkipBackend(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"
	cfg.TracesEnabled = false
	cfg.MetricsEnabled = false
	cfg.LogsEnabled = false

	spy := &spyBackend{Backend: fake.New()}
	p, err := telemetry.New(context.Background(), cfg, spy, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if spy.traceCalls != 0 {
		t.Errorf("TraceExporter called %d times, want 0 when TracesEnabled=false", spy.traceCalls)
	}
	if spy.metricCalls != 0 {
		t.Errorf("MetricReader called %d times, want 0 when MetricsEnabled=false", spy.metricCalls)
	}
	if spy.logCalls != 0 {
		t.Errorf("LogExporter called %d times, want 0 when LogsEnabled=false", spy.logCalls)
	}
	// A disabled log signal still yields a usable slog.Handler.
	if p.SlogHandler("test") == nil {
		t.Error("SlogHandler(\"test\") = nil with logging disabled, want a no-op-backed handler")
	}
	// A disabled signal still yields a usable (no-op) tracer/instruments,
	// not a nil one a caller would need to nil-check before every call.
	if p.Tracer() == nil {
		t.Error("Tracer() = nil with tracing disabled, want a no-op tracer")
	}
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Errorf("ForceFlush with tracing disabled: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestProvider_shutdownIdempotent(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test"
	p, err := telemetry.New(context.Background(), cfg, fake.New(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

// spyBackend wraps a real telemetry.Backend and counts how many times each
// method was invoked, so a test can assert a disabled signal or a
// rejected config never touched the backend at all.
type spyBackend struct {
	telemetry.Backend
	traceCalls  int
	metricCalls int
	logCalls    int
}

func (s *spyBackend) TraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	s.traceCalls++
	return s.Backend.TraceExporter(ctx)
}

func (s *spyBackend) MetricReader(ctx context.Context) (sdkmetric.Reader, error) {
	s.metricCalls++
	return s.Backend.MetricReader(ctx)
}

func (s *spyBackend) LogExporter(ctx context.Context) (sdklog.Exporter, error) {
	s.logCalls++
	return s.Backend.LogExporter(ctx)
}
