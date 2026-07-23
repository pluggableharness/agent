package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestInjectExtract_roundTrip(t *testing.T) {
	t.Parallel()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}

	original := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), original)

	carrier := propagation.MapCarrier{}
	Inject(ctx, carrier)

	if _, ok := carrier["traceparent"]; !ok {
		t.Fatal("Inject did not set a traceparent header")
	}

	extractedCtx := Extract(context.Background(), carrier)
	extracted := trace.SpanContextFromContext(extractedCtx)

	if extracted.TraceID() != original.TraceID() {
		t.Errorf("TraceID = %s, want %s", extracted.TraceID(), original.TraceID())
	}
	if extracted.SpanID() != original.SpanID() {
		t.Errorf("SpanID = %s, want %s", extracted.SpanID(), original.SpanID())
	}
	if !extracted.IsSampled() {
		t.Error("extracted span context lost the sampled flag")
	}
	if !extracted.IsRemote() {
		t.Error("extracted span context should be marked remote")
	}
}

func TestExtract_noHeader(t *testing.T) {
	t.Parallel()

	extractedCtx := Extract(context.Background(), propagation.MapCarrier{})
	if trace.SpanContextFromContext(extractedCtx).IsValid() {
		t.Error("Extract with no traceparent header produced a valid span context")
	}
}
