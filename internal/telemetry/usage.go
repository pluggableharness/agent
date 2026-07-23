package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// Usage is the exact set of values the kernel's cost writer computes for
// the cost_ledger table (state-backend.md §4.3). RecordUsage takes these
// as given rather than recomputing them: a second computation path for
// the same numbers is exactly the kind of divergence determinism.md's
// fallback-token-heuristic section warns about, even though usage figures
// themselves aren't persisted by this package. See CLAUDE.md.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	ModelID          string
}

// RecordUsage increments the Tokens/CostUSD counters from u and, if span
// is non-nil and recording, attaches the same values as GenAI
// semantic-convention attributes on span. Pass the active model-call span
// (from StartModelCall) so usage is visible on the trace as well as in the
// aggregate metrics. A zero-valued field is not recorded — a provider that
// only reports output tokens, say, doesn't spuriously add a zero data
// point for input tokens.
func (p *Provider) RecordUsage(ctx context.Context, span trace.Span, u Usage) {
	instr := p.instruments
	modelAttr := ModelIDKey.String(u.ModelID)

	if u.InputTokens > 0 {
		instr.Tokens.Add(ctx, u.InputTokens, metric.WithAttributes(TokenTypeKey.String(TokenTypeInput), modelAttr))
	}
	if u.OutputTokens > 0 {
		instr.Tokens.Add(ctx, u.OutputTokens, metric.WithAttributes(TokenTypeKey.String(TokenTypeOutput), modelAttr))
	}
	if u.CacheReadTokens > 0 {
		instr.Tokens.Add(ctx, u.CacheReadTokens, metric.WithAttributes(TokenTypeKey.String(TokenTypeCacheRead), modelAttr))
	}
	if u.CacheWriteTokens > 0 {
		instr.Tokens.Add(ctx, u.CacheWriteTokens, metric.WithAttributes(TokenTypeKey.String(TokenTypeCacheWrite), modelAttr))
	}
	if u.CostUSD > 0 {
		instr.CostUSD.Add(ctx, u.CostUSD, metric.WithAttributes(modelAttr))
	}

	if span == nil || !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{semconv.GenAIRequestModelKey.String(u.ModelID)}
	if u.InputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageInputTokensKey.Int64(u.InputTokens))
	}
	if u.OutputTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageOutputTokensKey.Int64(u.OutputTokens))
	}
	if u.CacheReadTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheReadInputTokensKey.Int64(u.CacheReadTokens))
	}
	if u.CacheWriteTokens > 0 {
		attrs = append(attrs, semconv.GenAIUsageCacheCreationInputTokensKey.Int64(u.CacheWriteTokens))
	}
	span.SetAttributes(attrs...)
}
