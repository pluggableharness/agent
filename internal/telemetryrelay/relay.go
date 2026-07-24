package telemetryrelay

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/pluggableharness/agent/internal/telemetry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

// serviceNameAttrKey is OTel's own resource semantic-convention key for a
// process's service name. Hardcoded rather than built via
// semconv.ServiceName(...).Key (which would require constructing a
// throwaway attribute.KeyValue just to read its Key back out) — this key
// name is one of OTel's oldest, most stable resource conventions.
const serviceNameAttrKey = "service.name"

// Relay translates plugin-relayed trace.v1.Span batches into OTLP
// ResourceSpans and uploads them via an otlptrace.Client — see doc.go.
type Relay struct {
	client otlptrace.Client
}

// New returns a Relay uploading through client (typically
// telemetry.Backend.TraceUploader's result for the plugin this Relay is
// dedicated to).
func New(client otlptrace.Client) *Relay {
	return &Relay{client: client}
}

// Upload translates spans into one ResourceSpans batch — grouped into
// one ScopeSpans per distinct InstrumentationScope a span declares, since
// OTLP's wire structure groups spans by (Resource, Scope) rather than
// carrying scope per span — stamped with producer's resource attributes
// (the same attribute.Key vocabulary internal/telemetry.BuildResource
// uses, so a relayed span's resource is indistinguishable from a directly
// exported one), and uploads it via the wrapped otlptrace.Client. Upload
// is a no-op returning nil for an empty spans slice — an ExportSpans
// caller that filters down to nothing has nothing to relay, not an error.
func (r *Relay) Upload(ctx context.Context, spans []*tracev1.Span, producer *commonv1.ProducerRef) error {
	if len(spans) == 0 {
		return nil
	}

	scopeSpans, err := groupByScope(spans)
	if err != nil {
		return fmt.Errorf("telemetryrelay: upload: %w", err)
	}

	rs := &tracepb.ResourceSpans{
		Resource:   resourceForProducer(producer),
		ScopeSpans: scopeSpans,
	}
	if err := r.client.UploadTraces(ctx, []*tracepb.ResourceSpans{rs}); err != nil {
		return fmt.Errorf("telemetryrelay: upload: %w", err)
	}
	return nil
}

// Stop releases the wrapped otlptrace.Client's connection. The caller
// that constructed this Relay (internal/kernelcallback, per plugin) owns
// calling this once, when the plugin's callback connection closes — see
// doc.go.
func (r *Relay) Stop(ctx context.Context) error {
	return r.client.Stop(ctx)
}

// scopeKey identifies one distinct InstrumentationScope for grouping.
type scopeKey struct {
	name    string
	version string
}

// groupByScope buckets spans into one *tracepb.ScopeSpans per distinct
// (name, version) InstrumentationScope, in first-seen order — stable
// across calls with the same input, since it never depends on Go's
// randomized map iteration for output ordering (.claude/rules/determinism.md).
func groupByScope(spans []*tracev1.Span) ([]*tracepb.ScopeSpans, error) {
	order := make([]scopeKey, 0)
	buckets := make(map[scopeKey]*tracepb.ScopeSpans)

	for _, s := range spans {
		converted, err := convertSpan(s)
		if err != nil {
			return nil, err
		}

		key := scopeKey{name: s.GetScope().GetName(), version: s.GetScope().GetVersion()}
		bucket, ok := buckets[key]
		if !ok {
			bucket = &tracepb.ScopeSpans{
				Scope: &commonpb.InstrumentationScope{Name: key.name, Version: key.version},
			}
			buckets[key] = bucket
			order = append(order, key)
		}
		bucket.Spans = append(bucket.Spans, converted)
	}

	out := make([]*tracepb.ScopeSpans, 0, len(order))
	for _, key := range order {
		out = append(out, buckets[key])
	}
	return out, nil
}

// resourceForProducer builds the OTLP Resource for a batch relayed on
// producer's behalf, using the exact attribute.Key vocabulary
// internal/telemetry.BuildResource uses for a directly-exported process's
// own Resource (ProducerCategoryKey/ProducerNameKey/ProducerVersionKey),
// so a relayed span's resource is indistinguishable from one that process
// exported itself. producer is never nil in practice — the kernel derives
// it from the authenticated callback connection before calling Upload —
// but a nil producer still yields a valid, attribute-less Resource rather
// than panicking.
func resourceForProducer(producer *commonv1.ProducerRef) *resourcepb.Resource {
	if producer == nil {
		return &resourcepb.Resource{}
	}
	return &resourcepb.Resource{
		Attributes: []*commonpb.KeyValue{
			stringKV(serviceNameAttrKey, producer.GetName()),
			stringKV(string(telemetry.ProducerCategoryKey), producer.GetCategory().String()),
			stringKV(string(telemetry.ProducerNameKey), producer.GetName()),
			stringKV(string(telemetry.ProducerVersionKey), producer.GetVersion()),
		},
	}
}

func stringKV(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}
