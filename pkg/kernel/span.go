package kernel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

// SpanExporter is an sdktrace.SpanExporter that relays completed spans to
// the kernel via ExportSpans (kernel-callbacks.md#exportspans,
// observability.md#the-relay-model) — the tracing half of this package's
// plugin-author-facing surface. A plugin author wires this into an
// ordinary sdktrace.TracerProvider (sdktrace.WithBatcher(exporter)) and
// writes normal tracer.Start(...) code; the relay transport is invisible.
//
// SpanExporter deliberately does NOT create or modify spans itself — it
// only translates already-completed ReadOnlySpan values into the wire
// trace.v1.Span shape, preserving every identity/timing field exactly
// (observability.md#span-relay-is-transparent's MUST NOT-alter rule
// applies just as much on this side of the relay as it does kernel-side).
type SpanExporter struct {
	client    *Client
	sessionID *string
}

// SpanExporterOption configures NewSpanExporter.
type SpanExporterOption func(*SpanExporter)

// WithExportSessionID attaches session_id to every ExportSpans call this
// exporter makes (kernel-callbacks.md#exportspans: MAY be omitted). Omit
// this option for spans produced outside any session context (Configure,
// startup).
func WithExportSessionID(sessionID string) SpanExporterOption {
	return func(e *SpanExporter) { e.sessionID = &sessionID }
}

// NewSpanExporter returns a SpanExporter relaying through c.
func (c *Client) NewSpanExporter(opts ...SpanExporterOption) *SpanExporter {
	e := &SpanExporter{client: c}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ExportSpans implements sdktrace.SpanExporter: translates spans into
// wire trace.v1.Span messages and relays them via one ExportSpans call.
// An empty spans slice is a no-op, matching ExportSpansRequest's own
// MUST-be-non-empty rule (nothing to send, not an error).
func (e *SpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	wireSpans := make([]*tracev1.Span, 0, len(spans))
	for _, s := range spans {
		ws, err := convertReadOnlySpan(s)
		if err != nil {
			return fmt.Errorf("kernel: export spans: %w", err)
		}
		wireSpans = append(wireSpans, ws)
	}

	_, err := e.client.raw.ExportSpans(ctx, &kernelv1.ExportSpansRequest{
		SessionId: e.sessionID,
		Spans:     wireSpans,
	})
	if err != nil {
		return fmt.Errorf("kernel: export spans: %w", err)
	}
	return nil
}

// Shutdown implements sdktrace.SpanExporter. There is no per-exporter
// resource to release — the underlying Client's connection lifetime is
// managed by whoever constructed it — so this is a no-op returning nil.
func (e *SpanExporter) Shutdown(context.Context) error {
	return nil
}

var _ sdktrace.SpanExporter = (*SpanExporter)(nil)

// convertReadOnlySpan translates one sdktrace.ReadOnlySpan into its wire
// trace.v1.Span equivalent, preserving every identity/timing field
// verbatim.
func convertReadOnlySpan(s sdktrace.ReadOnlySpan) (*tracev1.Span, error) {
	sc := s.SpanContext()
	traceID := sc.TraceID().String()
	spanID := sc.SpanID().String()

	var parentSpanID *string
	if parent := s.Parent(); parent.IsValid() {
		id := parent.SpanID().String()
		parentSpanID = &id
	}

	attrs, err := structFromKeyValues(s.Attributes())
	if err != nil {
		return nil, fmt.Errorf("span attributes: %w", err)
	}

	events, err := convertEvents(s.Events())
	if err != nil {
		return nil, err
	}
	links, err := convertLinks(s.Links())
	if err != nil {
		return nil, err
	}

	scope := s.InstrumentationScope()

	return &tracev1.Span{
		TraceId:      traceID,
		SpanId:       spanID,
		ParentSpanId: parentSpanID,
		Name:         s.Name(),
		Kind:         convertSpanKind(s.SpanKind()),
		StartTime:    timestamppb.New(s.StartTime()),
		EndTime:      timestamppb.New(s.EndTime()),
		Status:       convertStatus(s.Status()),
		Attributes:   attrs,
		Events:       events,
		Links:        links,
		Scope:        &tracev1.InstrumentationScope{Name: scope.Name, Version: scope.Version},
	}, nil
}

var spanKindToWire = map[oteltrace.SpanKind]tracev1.SpanKind{
	oteltrace.SpanKindUnspecified: tracev1.SpanKind_SPAN_KIND_UNSPECIFIED,
	oteltrace.SpanKindInternal:    tracev1.SpanKind_SPAN_KIND_INTERNAL,
	oteltrace.SpanKindServer:      tracev1.SpanKind_SPAN_KIND_SERVER,
	oteltrace.SpanKindClient:      tracev1.SpanKind_SPAN_KIND_CLIENT,
	oteltrace.SpanKindProducer:    tracev1.SpanKind_SPAN_KIND_PRODUCER,
	oteltrace.SpanKindConsumer:    tracev1.SpanKind_SPAN_KIND_CONSUMER,
}

func convertSpanKind(kind oteltrace.SpanKind) tracev1.SpanKind {
	if wire, ok := spanKindToWire[kind]; ok {
		return wire
	}
	return tracev1.SpanKind_SPAN_KIND_UNSPECIFIED
}

func convertStatus(status sdktrace.Status) *tracev1.Status {
	code := tracev1.StatusCode_STATUS_CODE_UNSPECIFIED
	switch status.Code {
	case otelcodes.Ok:
		code = tracev1.StatusCode_STATUS_CODE_OK
	case otelcodes.Error:
		code = tracev1.StatusCode_STATUS_CODE_ERROR
	case otelcodes.Unset:
		code = tracev1.StatusCode_STATUS_CODE_UNSPECIFIED
	}
	return &tracev1.Status{Code: code, Message: status.Description}
}

func convertEvents(events []sdktrace.Event) ([]*tracev1.SpanEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	out := make([]*tracev1.SpanEvent, 0, len(events))
	for _, e := range events {
		attrs, err := structFromKeyValues(e.Attributes)
		if err != nil {
			return nil, fmt.Errorf("event %q attributes: %w", e.Name, err)
		}
		out = append(out, &tracev1.SpanEvent{
			Name:       e.Name,
			Time:       timestamppb.New(e.Time),
			Attributes: attrs,
		})
	}
	return out, nil
}

func convertLinks(links []sdktrace.Link) ([]*tracev1.SpanLink, error) {
	if len(links) == 0 {
		return nil, nil
	}
	out := make([]*tracev1.SpanLink, 0, len(links))
	for _, l := range links {
		attrs, err := structFromKeyValues(l.Attributes)
		if err != nil {
			return nil, fmt.Errorf("link attributes: %w", err)
		}
		out = append(out, &tracev1.SpanLink{
			TraceId:    l.SpanContext.TraceID().String(),
			SpanId:     l.SpanContext.SpanID().String(),
			Attributes: attrs,
		})
	}
	return out, nil
}

// structFromKeyValues converts a list of OTel attribute.KeyValue pairs
// into a google.protobuf.Struct — the same Struct carve-out
// trace.v1.Span.attributes documents. Later duplicate keys win, matching
// attribute.NewSet's own last-value-wins convention for repeated keys.
func structFromKeyValues(attrs []attribute.KeyValue) (*structpb.Struct, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	fields := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		fields[string(kv.Key)] = attributeValueToAny(kv.Value)
	}
	// structpb.NewStruct is the one fallible step here (e.g. a STRING
	// attribute whose value happens not to be valid UTF-8) —
	// attributeValueToAny itself cannot fail, since every OTel attribute
	// kind converts to a structpb.NewValue-compatible Go type.
	s, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("attributes: %w", err)
	}
	return s, nil
}

// attributeValueToAny converts one OTel attribute.Value into a
// structpb.NewValue-compatible Go value. Slice-typed values (BOOLSLICE,
// INT64SLICE, FLOAT64SLICE, STRINGSLICE) convert element-wise into []any,
// since structpb.NewValue itself only accepts []any, not a concretely-typed
// slice.
func attributeValueToAny(v attribute.Value) any {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRING:
		return v.AsString()
	case attribute.BOOLSLICE:
		return toAnySlice(v.AsBoolSlice())
	case attribute.INT64SLICE:
		return toAnySlice(v.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		return toAnySlice(v.AsFloat64Slice())
	case attribute.STRINGSLICE:
		return toAnySlice(v.AsStringSlice())
	default:
		return v.String() // attribute.INVALID or any future kind: best-effort string form
	}
}

// toAnySlice converts a concretely-typed slice into []any, the shape
// structpb.NewValue requires for a ListValue.
func toAnySlice[T any](s []T) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
