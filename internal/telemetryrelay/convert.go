package telemetryrelay

import (
	"encoding/hex"
	"fmt"
	"sort"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/types/known/structpb"

	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

// spanKindToProto maps trace.v1.SpanKind to its identically-shaped OTLP
// wire enum — the two enums are deliberately parallel (trace.v1's own
// package comment), so this is a direct value-for-value translation, not
// a lossy narrowing.
var spanKindToProto = map[tracev1.SpanKind]tracepb.Span_SpanKind{
	tracev1.SpanKind_SPAN_KIND_UNSPECIFIED: tracepb.Span_SPAN_KIND_UNSPECIFIED,
	tracev1.SpanKind_SPAN_KIND_INTERNAL:    tracepb.Span_SPAN_KIND_INTERNAL,
	tracev1.SpanKind_SPAN_KIND_SERVER:      tracepb.Span_SPAN_KIND_SERVER,
	tracev1.SpanKind_SPAN_KIND_CLIENT:      tracepb.Span_SPAN_KIND_CLIENT,
	tracev1.SpanKind_SPAN_KIND_PRODUCER:    tracepb.Span_SPAN_KIND_PRODUCER,
	tracev1.SpanKind_SPAN_KIND_CONSUMER:    tracepb.Span_SPAN_KIND_CONSUMER,
}

// statusCodeToProto maps trace.v1.StatusCode to its identically-shaped
// OTLP wire enum.
var statusCodeToProto = map[tracev1.StatusCode]tracepb.Status_StatusCode{
	tracev1.StatusCode_STATUS_CODE_UNSPECIFIED: tracepb.Status_STATUS_CODE_UNSET,
	tracev1.StatusCode_STATUS_CODE_OK:          tracepb.Status_STATUS_CODE_OK,
	tracev1.StatusCode_STATUS_CODE_ERROR:       tracepb.Status_STATUS_CODE_ERROR,
}

// decodeSpanID decodes a hex-encoded W3C trace/span id field. An empty
// string decodes to nil (OTLP's documented "unset" representation for an
// optional id, e.g. a root span's absent parent_span_id), never a
// zero-length-but-non-nil byte slice.
func decodeSpanID(field, hexStr string) ([]byte, error) {
	if hexStr == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("telemetryrelay: convert: %s: %w", field, err)
	}
	return b, nil
}

// convertSpan translates one wire Span into its OTLP proto equivalent.
// The kernel MUST NOT alter identity/timing fields in this translation —
// see doc.go and observability.md#the-relay-model.
func convertSpan(s *tracev1.Span) (*tracepb.Span, error) {
	traceID, err := decodeSpanID("trace_id", s.GetTraceId())
	if err != nil {
		return nil, err
	}
	spanID, err := decodeSpanID("span_id", s.GetSpanId())
	if err != nil {
		return nil, err
	}
	var parentSpanID []byte
	if s.ParentSpanId != nil {
		parentSpanID, err = decodeSpanID("parent_span_id", s.GetParentSpanId())
		if err != nil {
			return nil, err
		}
	}

	events, err := convertEvents(s.GetEvents())
	if err != nil {
		return nil, err
	}
	links, err := convertLinks(s.GetLinks())
	if err != nil {
		return nil, err
	}
	attrs, err := structToKeyValues(s.GetAttributes())
	if err != nil {
		return nil, fmt.Errorf("telemetryrelay: convert: span attributes: %w", err)
	}

	return &tracepb.Span{
		TraceId:           traceID,
		SpanId:            spanID,
		ParentSpanId:      parentSpanID,
		Name:              s.GetName(),
		Kind:              spanKindToProto[s.GetKind()],
		StartTimeUnixNano: uint64(s.GetStartTime().AsTime().UnixNano()), //nolint:gosec // wall-clock nanos never negative in practice
		EndTimeUnixNano:   uint64(s.GetEndTime().AsTime().UnixNano()),   //nolint:gosec // wall-clock nanos never negative in practice
		Attributes:        attrs,
		Events:            events,
		Links:             links,
		Status:            convertStatus(s.GetStatus()),
	}, nil
}

func convertStatus(s *tracev1.Status) *tracepb.Status {
	if s == nil {
		return nil
	}
	return &tracepb.Status{
		Code:    statusCodeToProto[s.GetCode()],
		Message: s.GetMessage(),
	}
}

func convertEvents(events []*tracev1.SpanEvent) ([]*tracepb.Span_Event, error) {
	if len(events) == 0 {
		return nil, nil
	}
	out := make([]*tracepb.Span_Event, 0, len(events))
	for _, e := range events {
		attrs, err := structToKeyValues(e.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("telemetryrelay: convert: event %q attributes: %w", e.GetName(), err)
		}
		out = append(out, &tracepb.Span_Event{
			TimeUnixNano: uint64(e.GetTime().AsTime().UnixNano()), //nolint:gosec // wall-clock nanos never negative in practice
			Name:         e.GetName(),
			Attributes:   attrs,
		})
	}
	return out, nil
}

func convertLinks(links []*tracev1.SpanLink) ([]*tracepb.Span_Link, error) {
	if len(links) == 0 {
		return nil, nil
	}
	out := make([]*tracepb.Span_Link, 0, len(links))
	for _, l := range links {
		traceID, err := decodeSpanID("link trace_id", l.GetTraceId())
		if err != nil {
			return nil, err
		}
		spanID, err := decodeSpanID("link span_id", l.GetSpanId())
		if err != nil {
			return nil, err
		}
		attrs, err := structToKeyValues(l.GetAttributes())
		if err != nil {
			return nil, fmt.Errorf("telemetryrelay: convert: link attributes: %w", err)
		}
		out = append(out, &tracepb.Span_Link{
			TraceId:    traceID,
			SpanId:     spanID,
			Attributes: attrs,
		})
	}
	return out, nil
}

// structToKeyValues converts a google.protobuf.Struct into OTLP
// KeyValue/AnyValue pairs, sorted by key for deterministic output
// (.claude/rules/determinism.md's no-map-iteration-order rule — this
// package doesn't persist anything, but a stable translation makes this
// package's own tests, and any future golden-output comparison, reliable
// regardless of Go's randomized map iteration).
func structToKeyValues(s *structpb.Struct) ([]*commonpb.KeyValue, error) {
	fields := s.GetFields()
	if len(fields) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]*commonpb.KeyValue, 0, len(keys))
	for _, k := range keys {
		v, err := structValueToAnyValue(fields[k])
		if err != nil {
			return nil, fmt.Errorf("telemetryrelay: convert: attribute %q: %w", k, err)
		}
		out = append(out, &commonpb.KeyValue{Key: k, Value: v})
	}
	return out, nil
}

// structValueToAnyValue converts one google.protobuf.Value into its OTLP
// AnyValue equivalent, recursively for a nested struct or list.
func structValueToAnyValue(v *structpb.Value) (*commonpb.AnyValue, error) {
	if v == nil {
		return nil, nil
	}
	switch kind := v.GetKind().(type) {
	case *structpb.Value_NullValue, nil:
		return &commonpb.AnyValue{}, nil
	case *structpb.Value_BoolValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: kind.BoolValue}}, nil
	case *structpb.Value_NumberValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: kind.NumberValue}}, nil
	case *structpb.Value_StringValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: kind.StringValue}}, nil
	case *structpb.Value_StructValue:
		kvs, err := structToKeyValues(kind.StructValue)
		if err != nil {
			return nil, err
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: kvs}}}, nil
	case *structpb.Value_ListValue:
		values := kind.ListValue.GetValues()
		elems := make([]*commonpb.AnyValue, 0, len(values))
		for _, elem := range values {
			converted, err := structValueToAnyValue(elem)
			if err != nil {
				return nil, err
			}
			elems = append(elems, converted)
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: elems}}}, nil
	default:
		return nil, fmt.Errorf("telemetryrelay: convert: unsupported structpb.Value kind %T", kind)
	}
}
