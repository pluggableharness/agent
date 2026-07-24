package kernelcallback

import (
	"context"

	"github.com/pluggableharness/agent/internal/telemetry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	metricv1 "github.com/pluggableharness/agent/pkg/metric/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// metricKindToDynamic maps the wire metric.v1.MetricKind to this
// process's internal/telemetry.DynamicMetricKind — the two enums are
// deliberately parallel, so this is a direct value-for-value translation.
var metricKindToDynamic = map[metricv1.MetricKind]telemetry.DynamicMetricKind{
	metricv1.MetricKind_METRIC_KIND_COUNTER:         telemetry.DynamicMetricKindCounter,
	metricv1.MetricKind_METRIC_KIND_UP_DOWN_COUNTER: telemetry.DynamicMetricKindUpDownCounter,
	metricv1.MetricKind_METRIC_KIND_HISTOGRAM:       telemetry.DynamicMetricKindHistogram,
}

// ExportSpans implements the ExportSpans RPC (kernel-callbacks.md's
// ExportSpans): relays req.Spans to the operator's configured collector
// via s.relay, unmodified in every identity/timing field
// (observability.md#the-relay-model). Producer attribution comes from
// s.producer — the same server-derived identity every other RPC on this
// service uses — never from a field on the request.
func (s *Server) ExportSpans(ctx context.Context, req *kernelv1.ExportSpansRequest) (*kernelv1.ExportSpansResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackExportSpans(ctx, s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: export_spans", "spans", len(req.GetSpans()))

	spans := req.GetSpans()
	if len(spans) == 0 {
		err = status.Error(codes.InvalidArgument, "kernelcallback: export_spans: spans is required and must be non-empty")
		s.logger.WarnContext(ctx, "kernelcallback: export_spans: rejected", "err", err)
		return nil, err
	}

	if uploadErr := s.relay.Upload(ctx, spans, s.producer); uploadErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: export_spans: %v", uploadErr)
		s.logger.ErrorContext(ctx, "kernelcallback: export_spans: upload failed", "err", uploadErr)
		return nil, err
	}

	s.telemetry.Instruments().RelayedSpans.Add(ctx, int64(len(spans)))
	return &kernelv1.ExportSpansResult{}, nil
}

// RecordMetrics implements the RecordMetrics RPC (kernel-callbacks.md's
// RecordMetrics): records each observation against a kernel-owned,
// per-name instrument via s.telemetry.RecordDynamicMetric — deliberately
// not a transparent relay, see
// observability.md#the-tracing-metrics-asymmetry. The instrument name is
// built from s.producer's server-derived identity, exactly as Publish
// constructs an event-bus topic from the same identity — a plugin never
// supplies its own instrument namespace.
func (s *Server) RecordMetrics(ctx context.Context, req *kernelv1.RecordMetricsRequest) (*kernelv1.RecordMetricsResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackRecordMetrics(ctx, s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	metrics := req.GetMetrics()
	s.logger.DebugContext(ctx, "kernelcallback: record_metrics", "metrics", len(metrics))

	if len(metrics) == 0 {
		err = status.Error(codes.InvalidArgument, "kernelcallback: record_metrics: metrics is required and must be non-empty")
		s.logger.WarnContext(ctx, "kernelcallback: record_metrics: rejected", "err", err)
		return nil, err
	}

	for _, m := range metrics {
		kind, ok := metricKindToDynamic[m.GetKind()]
		if !ok {
			err = status.Errorf(codes.InvalidArgument, "kernelcallback: record_metrics: %q: kind is unspecified or unknown", m.GetName())
			s.logger.WarnContext(ctx, "kernelcallback: record_metrics: rejected", "err", err)
			return nil, err
		}

		name := producerScopedName(s.producer, m.GetName())
		value := metricValue(m)
		if recErr := s.telemetry.RecordDynamicMetric(ctx, name, kind, value, m.GetAttributes()); recErr != nil {
			err = status.Errorf(codes.InvalidArgument, "kernelcallback: record_metrics: %v", recErr)
			s.logger.WarnContext(ctx, "kernelcallback: record_metrics: rejected", "err", recErr)
			return nil, err
		}
	}

	return &kernelv1.RecordMetricsResult{}, nil
}

// metricValue extracts m's oneof value as a float64 — internal/telemetry's
// dynamic instruments are Float64-shaped regardless of which wire variant
// was set (dynamicmetric.go's own doc comment explains why).
func metricValue(m *metricv1.MetricRecord) float64 {
	if _, ok := m.GetValue().(*metricv1.MetricRecord_DoubleValue); ok {
		return m.GetDoubleValue()
	}
	return float64(m.GetIntValue())
}

// GetTelemetryConfig implements the GetTelemetryConfig RPC
// (kernel-callbacks.md's GetTelemetryConfig): reports whether tracing/
// metrics/logs are enabled and at what level/ratio, read from s.telemetry's
// own Config and s.logLevel, so a plugin doesn't have to guess from its
// own environment.
func (s *Server) GetTelemetryConfig(ctx context.Context, _ *kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackGetTelemetryConfig(ctx, s.producer)
	defer func() { telemetry.EndSpan(span, nil) }()

	s.logger.DebugContext(ctx, "kernelcallback: get_telemetry_config")

	cfg := s.telemetry.Config()
	return &kernelv1.GetTelemetryConfigResult{
		TracesEnabled:  cfg.TracesEnabled,
		MetricsEnabled: cfg.MetricsEnabled,
		LogsEnabled:    cfg.LogsEnabled,
		LogLevel:       s.logLevel,
		SamplingRatio:  cfg.SamplingRatio,
	}, nil
}

// producerScopedName builds a server-derived, dot-separated name from
// producer's identity plus a caller-declared leaf segment — the shared
// construction Publish uses for a bus topic and RecordMetrics uses for an
// instrument name. leaf is used verbatim; validating its shape (no "." or
// "*") is the caller's job (see eventbus.go's validateEventType for
// Publish's stricter version of this same rule) — RecordMetrics' metric
// name has no equivalent wire-level grammar restriction today, so this
// helper does not enforce one here.
func producerScopedName(p *commonv1.ProducerRef, leaf string) string {
	return "plugin." + categoryText(p.GetCategory()) + "." + p.GetName() + "." + leaf
}
