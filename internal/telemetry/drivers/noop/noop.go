// Package noop implements a telemetry.Backend that builds a real SDK
// pipeline (spans are created, sampled, and processed exactly as with any
// other backend) but discards everything at the export boundary — no
// bytes ever leave the process.
//
// This is distinct from telemetry.Config.TracesEnabled/MetricsEnabled=false,
// which bypasses the SDK pipeline entirely in favor of the true OTel
// no-op providers (zero span-creation overhead). Selecting this driver
// still exercises the sampler and span/metric plumbing — useful when an
// operator wants that code path running (e.g. to catch a panic in
// instrumentation code during testing) without shipping data anywhere.
//
// This is also the mandatory backend for replaying a session
// (internal/telemetry/CLAUDE.md): a replay MUST NOT re-emit production
// telemetry, and trace/span IDs are non-deterministic by construction, so
// replay telemetry can never be reproduced identically anyway.
package noop

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Backend is the telemetry.Backend that discards everything.
type Backend struct{}

// New returns a Backend. There is nothing to configure about discarding
// everything, so New takes no arguments.
func New() *Backend {
	return &Backend{}
}

// TraceExporter returns the SDK's own discard-everything span exporter.
func (*Backend) TraceExporter(context.Context) (sdktrace.SpanExporter, error) {
	return tracetest.NewNoopExporter(), nil
}

// MetricReader returns a ManualReader that is never collected — metrics
// recorded against it are tracked in bounded per-instrument aggregation
// state (per this package's cardinality rule, see
// internal/telemetry/CLAUDE.md) but never read out or exported anywhere.
func (*Backend) MetricReader(context.Context) (sdkmetric.Reader, error) {
	return sdkmetric.NewManualReader(), nil
}

// LogExporter returns a discarding sdklog.Exporter. The SDK ships no
// noop log exporter (unlike tracetest.NewNoopExporter for traces), so
// this is hand-written — three no-op methods, nothing to get wrong.
func (*Backend) LogExporter(context.Context) (sdklog.Exporter, error) {
	return noopLogExporter{}, nil
}

// noopLogExporter discards every log record it receives.
type noopLogExporter struct{}

func (noopLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (noopLogExporter) Shutdown(context.Context) error                { return nil }
func (noopLogExporter) ForceFlush(context.Context) error              { return nil }

// TraceUploader returns a discarding otlptrace.Client — the relay-path
// analog of TraceExporter/LogExporter's own discard-everything behavior.
// The SDK ships no noop otlptrace.Client (unlike tracetest.NewNoopExporter
// for a real sdktrace.SpanExporter), so this is hand-written, same as
// noopLogExporter above.
func (*Backend) TraceUploader(context.Context) (otlptrace.Client, error) {
	return noopTraceUploader{}, nil
}

// noopTraceUploader discards every relayed span it receives.
type noopTraceUploader struct{}

func (noopTraceUploader) Start(context.Context) error { return nil }
func (noopTraceUploader) Stop(context.Context) error  { return nil }
func (noopTraceUploader) UploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return nil
}

// Name returns "noop".
func (*Backend) Name() string { return "noop" }

var _ telemetry.Backend = (*Backend)(nil)
var _ sdklog.Exporter = noopLogExporter{}
var _ otlptrace.Client = noopTraceUploader{}
