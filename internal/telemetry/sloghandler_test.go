package telemetry_test

import (
	"context"
	"log/slog"
	"testing"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	intlog "github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

func flushedLogs(t *testing.T, p *telemetry.Provider, backend *fake.Backend) []sdklog.Record {
	t.Helper()
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	return backend.Logs.Records()
}

func TestSlogHandler_basic(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	logger := slog.New(p.SlogHandler("test-scope"))
	logger.InfoContext(context.Background(), "hello world")

	records := flushedLogs(t, p, backend)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if got := records[0].Body().AsString(); got != "hello world" {
		t.Errorf("Body = %q, want %q", got, "hello world")
	}
}

// TestSlogHandler_severityMapping confirms, empirically, that otelslog's
// default slog.Level-to-log.Severity conversion maps internal/log's custom
// LevelTrace/LevelFatal onto exactly SeverityTrace1/SeverityFatal1 — see
// sloghandler.go's doc comment for the arithmetic. If this test ever
// starts failing after an otelslog/internal/log version bump, that's the
// signal a remapping wrapper (documented as the fallback in the plan) is
// now needed — don't just loosen the assertion.
func TestSlogHandler_severityMapping(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	logger := slog.New(p.SlogHandler("test-scope"))
	ctx := context.Background()

	tests := []struct {
		name         string
		level        slog.Level
		wantSeverity otellog.Severity
	}{
		{"trace", intlog.LevelTrace, otellog.SeverityTrace1},
		{"debug", slog.LevelDebug, otellog.SeverityDebug1},
		{"info", slog.LevelInfo, otellog.SeverityInfo1},
		{"warn", slog.LevelWarn, otellog.SeverityWarn1},
		{"error", slog.LevelError, otellog.SeverityError1},
		{"fatal", intlog.LevelFatal, otellog.SeverityFatal1},
	}

	for _, tt := range tests {
		logger.Log(ctx, tt.level, tt.name)
	}

	records := flushedLogs(t, p, backend)
	if len(records) != len(tests) {
		t.Fatalf("len(records) = %d, want %d", len(records), len(tests))
	}
	for i, tt := range tests {
		if got := records[i].Severity(); got != tt.wantSeverity {
			t.Errorf("%s: Severity = %v, want %v", tt.name, got, tt.wantSeverity)
		}
	}
}

// TestSlogHandler_traceCorrelation is the main payoff this whole plan
// exists to prove: a log emitted while a span from the same Provider is
// active in ctx must carry that span's trace_id/span_id, with no
// propagation code of our own — the OTel Logs spec mandates
// Logger.Emit resolve trace context from ctx.
func TestSlogHandler_traceCorrelation(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	ctx, span := p.StartTurn(context.Background(), 0)
	logger := slog.New(p.SlogHandler("test-scope"))
	logger.InfoContext(ctx, "turn started")
	telemetry.EndSpan(span, nil)

	wantTraceID := span.SpanContext().TraceID()
	wantSpanID := span.SpanContext().SpanID()

	records := flushedLogs(t, p, backend)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if got := records[0].TraceID(); got != wantTraceID {
		t.Errorf("TraceID = %s, want %s", got, wantTraceID)
	}
	if got := records[0].SpanID(); got != wantSpanID {
		t.Errorf("SpanID = %s, want %s", got, wantSpanID)
	}
}

func TestSlogHandler_noCorrelationWithoutSpan(t *testing.T) {
	t.Parallel()
	p, backend := newTestProvider(t)

	logger := slog.New(p.SlogHandler("test-scope"))
	logger.InfoContext(context.Background(), "no span here")

	records := flushedLogs(t, p, backend)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].TraceID().IsValid() {
		t.Error("TraceID is valid with no active span in ctx, want invalid/zero")
	}
}
