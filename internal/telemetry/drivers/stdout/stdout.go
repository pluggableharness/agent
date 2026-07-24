// Package stdout implements the telemetry.Backend that writes spans and
// metrics as pretty-printed JSON to stdout — a dev/debug driver for
// running the kernel without a real OTLP collector nearby.
package stdout

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

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

// TraceUploader returns a Client that pretty-prints each relayed
// ResourceSpans batch to stdout — the relay-path analog of TraceExporter,
// since a relayed span (specifications/observability.md#the-relay-model)
// never passes through this process's own sdktrace.TracerProvider/
// stdouttrace exporter pipeline. There is no stdouttrace equivalent that
// accepts already-built ResourceSpans protos directly, so this is
// hand-written using protojson, matching this driver's existing
// pretty-print-for-a-human intent.
func (*Backend) TraceUploader(context.Context) (otlptrace.Client, error) {
	return stdoutTraceUploader{}, nil
}

// stdoutTraceUploader writes each relayed ResourceSpans, pretty-printed,
// to stdout.
type stdoutTraceUploader struct{}

func (stdoutTraceUploader) Start(context.Context) error { return nil }
func (stdoutTraceUploader) Stop(context.Context) error  { return nil }
func (stdoutTraceUploader) UploadTraces(_ context.Context, spans []*tracepb.ResourceSpans) error {
	marshaler := protojson.MarshalOptions{Multiline: true}
	for _, rs := range spans {
		b, err := marshaler.Marshal(rs)
		if err != nil {
			return fmt.Errorf("telemetry: stdout: trace uploader: marshal: %w", err)
		}
		if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("telemetry: stdout: trace uploader: write: %w", err)
		}
	}
	return nil
}

// Name returns "stdout".
func (*Backend) Name() string { return "stdout" }

var _ telemetry.Backend = (*Backend)(nil)
var _ otlptrace.Client = stdoutTraceUploader{}
