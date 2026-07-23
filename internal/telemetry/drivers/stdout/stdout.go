// Package stdout implements the telemetry.Backend that writes spans and
// metrics as pretty-printed JSON to stdout — a dev/debug driver for
// running the kernel without a real OTLP collector nearby.
package stdout

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Backend is the telemetry.Backend that writes to stdout.
type Backend struct{}

// New returns a Backend. Nothing about writing to stdout is configurable
// today (no filename, no rotation) — it's a debug aid, not a production
// sink.
func New() *Backend {
	return &Backend{}
}

// TraceExporter constructs a stdouttrace exporter with pretty-printing on,
// so a human reading the terminal can actually parse it.
func (*Backend) TraceExporter(context.Context) (sdktrace.SpanExporter, error) {
	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("telemetry: stdout: trace exporter: %w", err)
	}
	return exp, nil
}

// MetricReader constructs a stdoutmetric exporter wrapped in a
// PeriodicReader using the SDK's default interval (stdout is a debug aid;
// there's no operator-facing cadence to tune).
func (*Backend) MetricReader(context.Context) (sdkmetric.Reader, error) {
	exp, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("telemetry: stdout: metric exporter: %w", err)
	}
	return sdkmetric.NewPeriodicReader(exp), nil
}

// LogExporter constructs a stdoutlog exporter with pretty-printing on.
func (*Backend) LogExporter(context.Context) (sdklog.Exporter, error) {
	exp, err := stdoutlog.New(stdoutlog.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("telemetry: stdout: log exporter: %w", err)
	}
	return exp, nil
}

// Name returns "stdout".
func (*Backend) Name() string { return "stdout" }

var _ telemetry.Backend = (*Backend)(nil)
