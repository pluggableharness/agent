package telemetryrelay

import (
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	tracev1 "github.com/pluggableharness/agent/pkg/trace/proto/v1"
)

func TestDecodeSpanID(t *testing.T) {
	t.Parallel()

	t.Run("empty is nil, not zero-length", func(t *testing.T) {
		t.Parallel()
		got, err := decodeSpanID("field", "")
		if err != nil {
			t.Fatalf("decodeSpanID: %v", err)
		}
		if got != nil {
			t.Fatalf("decodeSpanID(\"\") = %v, want nil", got)
		}
	})

	t.Run("valid hex", func(t *testing.T) {
		t.Parallel()
		got, err := decodeSpanID("field", "0102030405060708")
		if err != nil {
			t.Fatalf("decodeSpanID: %v", err)
		}
		want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		if len(got) != len(want) {
			t.Fatalf("decodeSpanID = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("decodeSpanID = %v, want %v", got, want)
			}
		}
	})

	t.Run("invalid hex errors", func(t *testing.T) {
		t.Parallel()
		if _, err := decodeSpanID("field", "not-hex"); err == nil {
			t.Fatal("decodeSpanID(invalid) = nil error, want an error")
		}
	})
}

func TestConvertSpan_identityAndTimingPreserved(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second)
	parentID := "0102030405060708"

	span := &tracev1.Span{
		TraceId:      "0123456789abcdef0123456789abcdef",
		SpanId:       "fedcba9876543210",
		ParentSpanId: &parentID,
		Name:         "tool.execute",
		Kind:         tracev1.SpanKind_SPAN_KIND_CLIENT,
		StartTime:    timestamppb.New(start),
		EndTime:      timestamppb.New(end),
		Status:       &tracev1.Status{Code: tracev1.StatusCode_STATUS_CODE_OK},
		Scope:        &tracev1.InstrumentationScope{Name: "plugin.tool.github", Version: "1.0.0"},
	}

	got, err := convertSpan(span)
	if err != nil {
		t.Fatalf("convertSpan: %v", err)
	}

	if got.Name != "tool.execute" {
		t.Errorf("Name = %q, want tool.execute", got.Name)
	}
	if got.Kind != tracepb.Span_SPAN_KIND_CLIENT {
		t.Errorf("Kind = %v, want SPAN_KIND_CLIENT", got.Kind)
	}
	if got.StartTimeUnixNano != uint64(start.UnixNano()) {
		t.Errorf("StartTimeUnixNano = %d, want %d", got.StartTimeUnixNano, start.UnixNano())
	}
	if got.EndTimeUnixNano != uint64(end.UnixNano()) {
		t.Errorf("EndTimeUnixNano = %d, want %d", got.EndTimeUnixNano, end.UnixNano())
	}
	if got.Status.Code != tracepb.Status_STATUS_CODE_OK {
		t.Errorf("Status.Code = %v, want STATUS_CODE_OK", got.Status.Code)
	}
	if len(got.ParentSpanId) != 8 {
		t.Errorf("ParentSpanId len = %d, want 8", len(got.ParentSpanId))
	}
	if len(got.TraceId) != 16 {
		t.Errorf("TraceId len = %d, want 16 (32 hex chars)", len(got.TraceId))
	}
}

func TestConvertSpan_rootSpanHasNilParent(t *testing.T) {
	t.Parallel()

	span := &tracev1.Span{
		TraceId:   "0123456789abcdef0123456789abcdef",
		SpanId:    "fedcba9876543210",
		Name:      "session",
		Kind:      tracev1.SpanKind_SPAN_KIND_INTERNAL,
		StartTime: timestamppb.Now(),
		EndTime:   timestamppb.Now(),
		Status:    &tracev1.Status{},
	}

	got, err := convertSpan(span)
	if err != nil {
		t.Fatalf("convertSpan: %v", err)
	}
	if got.ParentSpanId != nil {
		t.Errorf("ParentSpanId = %v, want nil for a root span", got.ParentSpanId)
	}
}

func TestConvertSpan_invalidTraceIDErrors(t *testing.T) {
	t.Parallel()

	span := &tracev1.Span{
		TraceId:   "not-hex",
		SpanId:    "fedcba9876543210",
		StartTime: timestamppb.Now(),
		EndTime:   timestamppb.Now(),
	}
	if _, err := convertSpan(span); err == nil {
		t.Fatal("convertSpan(invalid trace_id) = nil error, want an error")
	}
}

func TestConvertEvents(t *testing.T) {
	t.Parallel()

	events := []*tracev1.SpanEvent{
		{Name: "retry", Time: timestamppb.Now()},
	}
	got, err := convertEvents(events)
	if err != nil {
		t.Fatalf("convertEvents: %v", err)
	}
	if len(got) != 1 || got[0].Name != "retry" {
		t.Fatalf("convertEvents = %+v, want one event named retry", got)
	}
}

func TestConvertEvents_empty(t *testing.T) {
	t.Parallel()
	got, err := convertEvents(nil)
	if err != nil {
		t.Fatalf("convertEvents: %v", err)
	}
	if got != nil {
		t.Fatalf("convertEvents(nil) = %v, want nil", got)
	}
}

func TestConvertLinks(t *testing.T) {
	t.Parallel()

	links := []*tracev1.SpanLink{
		{TraceId: "0123456789abcdef0123456789abcdef", SpanId: "fedcba9876543210"},
	}
	got, err := convertLinks(links)
	if err != nil {
		t.Fatalf("convertLinks: %v", err)
	}
	if len(got) != 1 || len(got[0].TraceId) != 16 || len(got[0].SpanId) != 8 {
		t.Fatalf("convertLinks = %+v, want one link with 16-byte trace id / 8-byte span id", got)
	}
}

func TestConvertLinks_invalidSpanIDErrors(t *testing.T) {
	t.Parallel()
	links := []*tracev1.SpanLink{{TraceId: "0123456789abcdef0123456789abcdef", SpanId: "not-hex"}}
	if _, err := convertLinks(links); err == nil {
		t.Fatal("convertLinks(invalid span_id) = nil error, want an error")
	}
}

func TestStructToKeyValues(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		got, err := structToKeyValues(nil)
		if err != nil {
			t.Fatalf("structToKeyValues: %v", err)
		}
		if got != nil {
			t.Fatalf("structToKeyValues(nil) = %v, want nil", got)
		}
	})

	t.Run("sorted by key regardless of map order", func(t *testing.T) {
		t.Parallel()
		s, err := structpb.NewStruct(map[string]any{
			"zebra": "z",
			"alpha": "a",
			"mid":   "m",
		})
		if err != nil {
			t.Fatalf("structpb.NewStruct: %v", err)
		}
		got, err := structToKeyValues(s)
		if err != nil {
			t.Fatalf("structToKeyValues: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len(got) = %d, want 3", len(got))
		}
		wantOrder := []string{"alpha", "mid", "zebra"}
		for i, k := range wantOrder {
			if got[i].Key != k {
				t.Fatalf("got[%d].Key = %q, want %q", i, got[i].Key, k)
			}
		}
	})

	t.Run("every value kind converts", func(t *testing.T) {
		t.Parallel()
		s, err := structpb.NewStruct(map[string]any{
			"str":    "hello",
			"num":    float64(42),
			"flag":   true,
			"nested": map[string]any{"inner": "v"},
			"list":   []any{"a", "b"},
		})
		if err != nil {
			t.Fatalf("structpb.NewStruct: %v", err)
		}
		got, err := structToKeyValues(s)
		if err != nil {
			t.Fatalf("structToKeyValues: %v", err)
		}
		if len(got) != 5 {
			t.Fatalf("len(got) = %d, want 5", len(got))
		}

		byKey := make(map[string]*commonpb.AnyValue, len(got))
		for _, kv := range got {
			byKey[kv.Key] = kv.Value
		}

		if byKey["str"].GetStringValue() != "hello" {
			t.Errorf("str = %v, want hello", byKey["str"])
		}
		if byKey["num"].GetDoubleValue() != 42 {
			t.Errorf("num = %v, want 42", byKey["num"])
		}
		if !byKey["flag"].GetBoolValue() {
			t.Errorf("flag = %v, want true", byKey["flag"])
		}
		if inner := byKey["nested"].GetKvlistValue(); inner == nil || len(inner.Values) != 1 || inner.Values[0].Key != "inner" {
			t.Errorf("nested = %v, want a one-entry kvlist keyed \"inner\"", byKey["nested"])
		}
		if list := byKey["list"].GetArrayValue(); list == nil || len(list.Values) != 2 {
			t.Errorf("list = %v, want a two-element array", byKey["list"])
		}
	})
}

func TestStructValueToAnyValue_nil(t *testing.T) {
	t.Parallel()
	got, err := structValueToAnyValue(nil)
	if err != nil {
		t.Fatalf("structValueToAnyValue(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("structValueToAnyValue(nil) = %v, want nil", got)
	}
}
