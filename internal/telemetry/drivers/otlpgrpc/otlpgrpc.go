// Package otlpgrpc implements the telemetry.Backend that exports over
// OTLP/gRPC — the default production driver (telemetry.DefaultConfig.Backend),
// matching this repo's existing gRPC-everywhere posture (grpc.md).
package otlpgrpc

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Backend is the telemetry.Backend for OTLP/gRPC export.
type Backend struct {
	cfg telemetry.Config
}

// New returns a Backend configured from cfg (cfg.Endpoint, cfg.Insecure,
// cfg.ExportInterval).
func New(cfg telemetry.Config) *Backend {
	return &Backend{cfg: cfg}
}

// TraceExporter constructs an otlptracegrpc exporter. It returns an
// unstarted exporter's constructor error, if any (a malformed endpoint,
// for instance) — the exporter itself connects lazily/asynchronously per
// otlptracegrpc's own retry behavior, so New succeeding here does not mean
// a collector is reachable yet.
func (b *Backend) TraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlpgrpc: trace exporter: %w", err)
	}
	return exp, nil
}

// MetricReader constructs an otlpmetricgrpc exporter wrapped in a
// PeriodicReader firing every cfg.ExportInterval.
func (b *Backend) MetricReader(ctx context.Context) (sdkmetric.Reader, error) {
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlpgrpc: metric exporter: %w", err)
	}
	return sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(b.cfg.ExportInterval)), nil
}

// LogExporter constructs an otlploggrpc exporter — no periodic-reader
// wrapping needed here (unlike MetricReader): the log SDK's batching lives
// on the processor side (sdklog.NewBatchProcessor, constructed by
// telemetry.New itself), not the exporter.
func (b *Backend) LogExporter(ctx context.Context) (sdklog.Exporter, error) {
	opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	exp, err := otlploggrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlpgrpc: log exporter: %w", err)
	}
	return exp, nil
}

// TraceUploader constructs an otlptracegrpc raw Client and starts it —
// see telemetry.Backend.TraceUploader's doc comment for why this bypasses
// otlptracegrpc.New's usual sdktrace.SpanExporter wrapping entirely. The
// caller owns calling Client.Stop when done relaying.
func (b *Backend) TraceUploader(ctx context.Context) (otlptrace.Client, error) {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	client := otlptracegrpc.NewClient(opts...)
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("telemetry: otlpgrpc: trace uploader: %w", err)
	}
	return client, nil
}

// Name returns "otlpgrpc".
func (*Backend) Name() string { return "otlpgrpc" }

var _ telemetry.Backend = (*Backend)(nil)
