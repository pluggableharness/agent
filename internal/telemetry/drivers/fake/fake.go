// Package fake implements the telemetry.Backend test double: a
// go-testing.md-mandated fake, not a mock. It records every span
// in-memory via the SDK's own tracetest.InMemoryExporter, exposes a
// ManualReader a test triggers explicitly for metrics, and records every
// log record in-memory via a hand-written recorder (the SDK ships no
// logs equivalent of tracetest.InMemoryExporter as of sdk/log v0.20.0),
// so a test can assert exactly what internal/telemetry's
// Start*/RecordUsage/SlogHandler calls produced without a real OTLP
// collector.
package fake

import (
	"context"
	"sync"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Backend is the telemetry.Backend test double. Spans is the exporter
// spans were recorded into (call ForceFlush on the owning
// telemetry.Provider first, then Spans.GetSpans()); Metrics is the reader
// a test calls Collect on directly to pull current instrument state; Logs
// is the recorder log records were exported into (ForceFlush first, then
// Logs.Records()).
type Backend struct {
	Spans   *tracetest.InMemoryExporter
	Metrics *sdkmetric.ManualReader
	Logs    *LogRecorder
}

// New returns a Backend with fresh, empty recorders.
func New() *Backend {
	return &Backend{
		Spans:   tracetest.NewInMemoryExporter(),
		Metrics: sdkmetric.NewManualReader(),
		Logs:    NewLogRecorder(),
	}
}

// TraceExporter returns b.Spans.
func (b *Backend) TraceExporter(context.Context) (sdktrace.SpanExporter, error) {
	return b.Spans, nil
}

// MetricReader returns b.Metrics.
func (b *Backend) MetricReader(context.Context) (sdkmetric.Reader, error) {
	return b.Metrics, nil
}

// LogExporter returns b.Logs.
func (b *Backend) LogExporter(context.Context) (sdklog.Exporter, error) {
	return b.Logs, nil
}

// Name returns "fake".
func (*Backend) Name() string { return "fake" }

var _ telemetry.Backend = (*Backend)(nil)

// LogRecorder is a hand-written in-memory sdklog.Exporter test double
// (go-testing.md: "fakes are hand-written"), used because sdk/log v0.20.0
// ships no logs equivalent of tracetest.InMemoryExporter.
type LogRecorder struct {
	mu      sync.Mutex
	records []sdklog.Record
}

// NewLogRecorder returns an empty LogRecorder.
func NewLogRecorder() *LogRecorder {
	return &LogRecorder{}
}

// Export appends a clone of each record — sdklog.Exporter's contract
// forbids retaining the slice or its records without cloning first
// (exporter.go: "Before modifying a Record, the implementation must use
// Record.Clone").
func (r *LogRecorder) Export(_ context.Context, records []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		r.records = append(r.records, rec.Clone())
	}
	return nil
}

// Shutdown is a no-op; nothing needs releasing.
func (r *LogRecorder) Shutdown(context.Context) error { return nil }

// ForceFlush is a no-op; Export already applies synchronously.
func (r *LogRecorder) ForceFlush(context.Context) error { return nil }

// Records returns a copy of every record recorded so far.
func (r *LogRecorder) Records() []sdklog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sdklog.Record, len(r.records))
	copy(out, r.records)
	return out
}

// Reset clears all recorded records.
func (r *LogRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = nil
}

var _ sdklog.Exporter = (*LogRecorder)(nil)
