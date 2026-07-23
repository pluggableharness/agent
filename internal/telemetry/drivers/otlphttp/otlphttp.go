// Package otlphttp implements the telemetry.Backend that exports over
// OTLP/HTTP, for an operator whose collector isn't reachable over gRPC
// (e.g. behind an HTTP-only ingress/proxy).
package otlphttp

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Backend is the telemetry.Backend for OTLP/HTTP export.
type Backend struct {
	cfg telemetry.Config
}

// New returns a Backend configured from cfg (cfg.Endpoint, cfg.Insecure,
// cfg.ExportInterval).
func New(cfg telemetry.Config) *Backend {
	return &Backend{cfg: cfg}
}

// TraceExporter constructs an otlptracehttp exporter.
func (b *Backend) TraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlphttp: trace exporter: %w", err)
	}
	return exp, nil
}

// MetricReader constructs an otlpmetrichttp exporter wrapped in a
// PeriodicReader firing every cfg.ExportInterval.
func (b *Backend) MetricReader(ctx context.Context) (sdkmetric.Reader, error) {
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlphttp: metric exporter: %w", err)
	}
	return sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(b.cfg.ExportInterval)), nil
}

// LogExporter constructs an otlploghttp exporter — no periodic-reader
// wrapping needed here (unlike MetricReader): the log SDK's batching lives
// on the processor side (sdklog.NewBatchProcessor, constructed by
// telemetry.New itself), not the exporter.
func (b *Backend) LogExporter(ctx context.Context) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{otlploghttp.WithEndpoint(b.cfg.Endpoint)}
	if b.cfg.Insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlphttp: log exporter: %w", err)
	}
	return exp, nil
}

// Name returns "otlphttp".
func (*Backend) Name() string { return "otlphttp" }

var _ telemetry.Backend = (*Backend)(nil)
