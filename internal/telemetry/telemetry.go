package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	otellog "go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// tracerName and meterName identify this package's own instrumentation
// scope, distinct from any name a plugin or downstream library might use
// for its own spans/instruments.
const (
	tracerName = "github.com/pluggableharness/agent/kernel"
	meterName  = "github.com/pluggableharness/agent/kernel"
)

// Backend is the swappable exporter family — the driver-pattern interface
// (go-layout.md) for this package. A driver returns OTel's own exporter
// and reader interfaces directly rather than a parallel abstraction: the
// SDK's SpanExporter/Reader are already the right seam, so wrapping them
// again would be redundant indirection (see internal/log/CLAUDE.md's
// identical reasoning about not wrapping slog.Handler).
type Backend interface {
	// TraceExporter returns the span exporter to batch spans through.
	TraceExporter(ctx context.Context) (sdktrace.SpanExporter, error)

	// MetricReader returns the metric reader to register with the meter
	// provider.
	MetricReader(ctx context.Context) (sdkmetric.Reader, error)

	// LogExporter returns the log-record exporter to batch log records
	// through — the seam that lets Provider.SlogHandler (sloghandler.go)
	// carry internal/log's plugin-relayed entries, and the kernel's own
	// log/slog output (internal/CLAUDE.md), into this backend.
	LogExporter(ctx context.Context) (sdklog.Exporter, error)

	// TraceUploader returns an already-started otlptrace.Client for
	// relaying a plugin's own already-completed spans
	// (specifications/observability.md#the-relay-model) to this backend's
	// collector, bypassing the SDK's TracerProvider/span-creation pipeline
	// entirely — a relayed span already has its own trace_id/span_id/
	// timestamps from the plugin's own SDK, and re-creating it through
	// this process's own tracer would silently reassign those, severing
	// it from the parent/child relationships it already had. Unlike
	// TraceExporter (which telemetry.New wraps in a
	// sdktrace.TracerProvider this process starts and stops), the
	// returned Client is not currently owned by Provider — the caller
	// that requests it (internal/telemetryrelay) is responsible for
	// calling Client.Stop when it's done, mirroring what
	// otlptrace.Exporter would normally do internally. TraceUploader
	// itself calls Client.Start before returning, so the returned Client
	// is immediately ready for UploadTraces.
	TraceUploader(ctx context.Context) (otlptrace.Client, error)

	// Name identifies the driver, for error messages and diagnostics.
	Name() string
}

// Provider owns this process's tracer and meter providers plus its
// pre-constructed Instruments. It is the single object cmd/agent (or a
// plugin's pkg/telemetry.Bootstrap) constructs via New and tears down via
// Shutdown.
type Provider struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
	lp *sdklog.LoggerProvider

	// tracerProvider, meterProvider, and loggerProvider are the
	// interface-typed views handed to otelgrpc's stats handlers
	// (grpchooks.go) and to Provider.SlogHandler (sloghandler.go) — either
	// tp/mp/lp themselves (signal enabled) or the OTel no-op provider
	// (signal disabled). Kept distinct from tp/mp/lp so callers never need
	// to duplicate the enabled/disabled selection logic already done in New.
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	loggerProvider otellog.LoggerProvider

	tracer         trace.Tracer
	instruments    *Instruments
	dynamicMetrics *dynamicMetrics

	shutdownOnce sync.Once
	shutdownErr  error
}

// New builds a Provider from cfg using backend for the underlying
// exporter/reader. producer identifies the calling process's own plugin
// identity for resource attribution (BuildResource) — pass nil for the
// kernel process itself, which has no plugin identity.
//
// When cfg.TracesEnabled or cfg.MetricsEnabled is false, that signal is
// wired to an OTel no-op provider instead of ever calling backend for it —
// a disabled signal never constructs an exporter/reader at all, not even
// a discarding one.
func New(ctx context.Context, cfg Config, backend Backend, producer *commonv1.ProducerRef) (*Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, ErrNilBackend
	}

	res, err := BuildResource(ctx, cfg, producer)
	if err != nil {
		return nil, fmt.Errorf("telemetry: new: %w", err)
	}

	p := &Provider{}

	if cfg.TracesEnabled {
		exporter, err := backend.TraceExporter(ctx)
		if err != nil {
			return nil, fmt.Errorf("telemetry: new: trace exporter (%s): %w", backend.Name(), err)
		}
		sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplingRatio))
		p.tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sampler),
		)
		p.tracerProvider = p.tp
	} else {
		p.tracerProvider = tracenoop.NewTracerProvider()
	}
	p.tracer = p.tracerProvider.Tracer(tracerName)

	if cfg.MetricsEnabled {
		reader, err := backend.MetricReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("telemetry: new: metric reader (%s): %w", backend.Name(), err)
		}
		p.mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(reader),
			sdkmetric.WithResource(res),
		)
		p.meterProvider = p.mp
	} else {
		p.meterProvider = metricnoop.NewMeterProvider()
	}

	if cfg.LogsEnabled {
		exporter, err := backend.LogExporter(ctx)
		if err != nil {
			return nil, fmt.Errorf("telemetry: new: log exporter (%s): %w", backend.Name(), err)
		}
		p.lp = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
			sdklog.WithResource(res),
		)
		p.loggerProvider = p.lp
	} else {
		p.loggerProvider = lognoop.NewLoggerProvider()
	}

	instruments, err := newInstruments(p.meterProvider.Meter(meterName))
	if err != nil {
		return nil, fmt.Errorf("telemetry: new: instruments: %w", err)
	}
	p.instruments = instruments
	p.dynamicMetrics = newDynamicMetrics(p.meterProvider.Meter(meterName))

	return p, nil
}

// Shutdown flushes and closes the underlying tracer/meter/logger
// providers. Idempotent — safe to call more than once, returning the
// first call's result every time. This guard is this package's own, not
// the SDK's: sdktrace.TracerProvider.Shutdown is internally idempotent,
// but sdkmetric.MeterProvider.Shutdown is NOT (it unconditionally
// re-invokes the underlying reader's Shutdown, which errors "reader is
// shutdown" on a second call) — see CLAUDE.md. Safe on a Provider whose
// signals were disabled (their fields are nil and are skipped).
func (p *Provider) Shutdown(ctx context.Context) error {
	p.shutdownOnce.Do(func() {
		var errs []error
		if p.tp != nil {
			if err := p.tp.Shutdown(ctx); err != nil {
				errs = append(errs, fmt.Errorf("telemetry: shutdown: tracer provider: %w", err))
			}
		}
		if p.mp != nil {
			if err := p.mp.Shutdown(ctx); err != nil {
				errs = append(errs, fmt.Errorf("telemetry: shutdown: meter provider: %w", err))
			}
		}
		if p.lp != nil {
			if err := p.lp.Shutdown(ctx); err != nil {
				errs = append(errs, fmt.Errorf("telemetry: shutdown: logger provider: %w", err))
			}
		}
		p.shutdownErr = errors.Join(errs...)
	})
	return p.shutdownErr
}

// ForceFlush flushes any spans queued in the batch span processor without
// waiting for its normal export interval, and any log records queued in
// the batch log processor likewise. Shutdown already flushes before
// closing, so this is for a caller that needs pending spans/logs visible
// to its backend before shutting down entirely — a test asserting against
// a fake backend's recorded spans/logs, or an operator-triggered
// "flush now" diagnostic. A no-op for a disabled signal (its provider
// field is nil).
func (p *Provider) ForceFlush(ctx context.Context) error {
	var errs []error
	if p.tp != nil {
		if err := p.tp.ForceFlush(ctx); err != nil {
			errs = append(errs, fmt.Errorf("telemetry: force flush: tracer provider: %w", err))
		}
	}
	if p.lp != nil {
		if err := p.lp.ForceFlush(ctx); err != nil {
			errs = append(errs, fmt.Errorf("telemetry: force flush: logger provider: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Tracer returns this process's tracer for starting spans. Callers
// generally use the Start* helpers in span.go instead of calling this
// directly.
func (p *Provider) Tracer() trace.Tracer {
	return p.tracer
}

// Instruments returns the pre-constructed metric instruments.
func (p *Provider) Instruments() *Instruments {
	return p.instruments
}
