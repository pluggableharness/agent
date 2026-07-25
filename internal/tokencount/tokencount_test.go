package tokencount

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func textBlock(s string) *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: s}}}
}

func toolUseBlock() *contentv1.ContentBlock {
	return &contentv1.ContentBlock{Block: &contentv1.ContentBlock_ToolUse{ToolUse: &contentv1.ToolUseBlock{Name: "x"}}}
}

func TestFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		blocks []*contentv1.ContentBlock
		want   int64
	}{
		{
			name:   "empty input",
			blocks: nil,
			want:   0,
		},
		{
			name:   "ascii text, exact multiple of 4",
			blocks: []*contentv1.ContentBlock{textBlock("12345678")}, // 8 bytes -> ceil(8/4)=2
			want:   2,
		},
		{
			name:   "ascii text, remainder rounds up",
			blocks: []*contentv1.ContentBlock{textBlock("123456789")}, // 9 bytes -> ceil(9/4)=3
			want:   3,
		},
		{
			name: "multi-byte UTF-8 drives byte length not rune count",
			// "日本語" is 3 runes but 9 UTF-8 bytes (each CJK ideograph
			// encodes to 3 bytes) — the determinism-hazard regression
			// case: a rune-counting implementation would compute
			// ceil(3/4)=1, but the spec mandates byte length, giving
			// ceil(9/4)=3. This is the exact byte-vs-rune divergence
			// determinism.md calls out by name.
			blocks: []*contentv1.ContentBlock{textBlock("日本語")},
			want:   3,
		},
		{
			name: "multiple text blocks summed",
			blocks: []*contentv1.ContentBlock{
				textBlock("1234"), // 4 bytes
				textBlock("12"),   // 2 bytes
				textBlock("1"),    // 1 byte
			}, // total 7 bytes -> ceil(7/4)=2
			want: 2,
		},
		{
			name: "non-text blocks contribute zero",
			blocks: []*contentv1.ContentBlock{
				toolUseBlock(),
				textBlock("1234"), // 4 bytes
				toolUseBlock(),
			},
			want: 1,
		},
		{
			name:   "nil block in slice contributes zero, not a panic",
			blocks: []*contentv1.ContentBlock{nil, textBlock("1234")},
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Fallback(tt.blocks)
			if got != tt.want {
				t.Errorf("Fallback() = %d, want %d", got, tt.want)
			}
		})
	}
}

// fallbackCount force-flushes prov and returns the current
// pluggableharness.token_count.fallbacks value recorded against reason in
// backend, summed across every matching data point.
func fallbackCount(t *testing.T, prov *telemetry.Provider, backend *fake.Backend, reason string) int64 {
	t.Helper()
	if err := prov.ForceFlush(t.Context()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "pluggableharness.token_count.fallbacks" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				for _, attr := range dp.Attributes.ToSlice() {
					if string(attr.Key) == "pluggableharness.tokencount.fallback_reason" && attr.Value.AsString() == reason {
						total += dp.Value
					}
				}
			}
		}
	}
	return total
}

func TestCounter_Count_nilRef(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger()
	prov, backend := testProviderWithBackend(t)
	c := NewCounter(newFakeLookup(), prov, logger)

	blocks := []*contentv1.ContentBlock{textBlock("1234")}
	count, exact := c.Count(t.Context(), blocks, nil)
	if exact {
		t.Error("exact = true, want false")
	}
	if want := Fallback(blocks); count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
	if got := fallbackCount(t, prov, backend, "no_model_ref"); got != 1 {
		t.Errorf("no_model_ref fallback metric = %d, want 1", got)
	}
}

func TestCounter_Count_emptyProvider(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger()
	prov, backend := testProviderWithBackend(t)
	c := NewCounter(newFakeLookup(), prov, logger)

	blocks := []*contentv1.ContentBlock{textBlock("1234")}
	count, exact := c.Count(t.Context(), blocks, &modelv1.ModelRef{Provider: "", Id: "whatever"})
	if exact {
		t.Error("exact = true, want false")
	}
	if want := Fallback(blocks); count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
	if got := fallbackCount(t, prov, backend, "no_model_ref"); got != 1 {
		t.Errorf("no_model_ref fallback metric = %d, want 1", got)
	}
}

func TestCounter_Count_providerAbsent(t *testing.T) {
	t.Parallel()
	logger, handler := testLogger()
	prov, backend := testProviderWithBackend(t)
	c := NewCounter(newFakeLookup(), prov, logger)

	blocks := []*contentv1.ContentBlock{textBlock("1234")}
	count, exact := c.Count(t.Context(), blocks, &modelv1.ModelRef{Provider: "not-loaded", Id: "m1"})
	if exact {
		t.Error("exact = true, want false")
	}
	if want := Fallback(blocks); count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
	if !handler.hasLevel(slog.LevelDebug) {
		t.Error("expected a DEBUG log for provider-absent fallback")
	}
	if got := fallbackCount(t, prov, backend, "provider_absent"); got != 1 {
		t.Errorf("provider_absent fallback metric = %d, want 1", got)
	}
}

func TestCounter_Count_success(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger()
	client := &fakeModelClient{
		countTokensFunc: func(context.Context, *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error) {
			return &modelv1.CountTokensResponse{Count: 42}, nil
		},
	}
	lookup := newFakeLookup().with("anthropic", client)
	c := NewCounter(lookup, testProvider(t), logger)

	blocks := []*contentv1.ContentBlock{textBlock("hello world")}
	count, exact := c.Count(t.Context(), blocks, &modelv1.ModelRef{Provider: "anthropic", Id: "claude"})
	if !exact {
		t.Error("exact = false, want true")
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
	if len(client.calls) != 1 {
		t.Fatalf("CountTokens calls = %d, want 1", len(client.calls))
	}
	if got, want := client.calls[0].GetText(), "hello world"; got != want {
		t.Errorf("request text = %q, want %q", got, want)
	}
	if got, want := client.calls[0].GetModelId(), "claude"; got != want {
		t.Errorf("request model_id = %q, want %q", got, want)
	}
}

func TestCounter_Count_unimplementedThenMemoized(t *testing.T) {
	t.Parallel()
	logger, handler := testLogger()
	client := &fakeModelClient{
		countTokensFunc: func(context.Context, *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error) {
			return nil, unimplementedErr()
		},
	}
	lookup := newFakeLookup().with("anthropic", client)
	c := NewCounter(lookup, testProvider(t), logger)

	blocks := []*contentv1.ContentBlock{textBlock("1234")}
	ref := &modelv1.ModelRef{Provider: "anthropic", Id: "claude"}

	count, exact := c.Count(t.Context(), blocks, ref)
	if exact {
		t.Error("exact = true, want false")
	}
	if want := Fallback(blocks); count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
	if len(client.calls) != 1 {
		t.Fatalf("CountTokens calls after first Count = %d, want 1", len(client.calls))
	}
	if !handler.hasLevel(slog.LevelDebug) {
		t.Error("expected a DEBUG log for unimplemented memoization")
	}

	// Swap in a provider mapping to panickingClient: if memoization
	// actually short-circuits the round trip, panickingClient.CountTokens
	// is never invoked and this call proceeds straight to the fallback.
	// If memoization is broken, panickingClient panics and fails the test.
	lookup.with("anthropic", panickingClient{})

	count2, exact2 := c.Count(t.Context(), blocks, ref)
	if exact2 {
		t.Error("exact = true on second call, want false (memoized unimplemented)")
	}
	if want := Fallback(blocks); count2 != want {
		t.Errorf("count = %d, want %d", count2, want)
	}
}

func TestCounter_Count_transientErrorNotMemoized(t *testing.T) {
	t.Parallel()
	logger, handler := testLogger()
	var calls int
	var mu sync.Mutex
	client := &fakeModelClient{
		countTokensFunc: func(context.Context, *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return nil, unavailableErr()
		},
	}
	lookup := newFakeLookup().with("anthropic", client)
	c := NewCounter(lookup, testProvider(t), logger)

	blocks := []*contentv1.ContentBlock{textBlock("1234")}
	ref := &modelv1.ModelRef{Provider: "anthropic", Id: "claude"}

	if _, exact := c.Count(t.Context(), blocks, ref); exact {
		t.Error("exact = true, want false")
	}
	if !handler.hasLevel(slog.LevelWarn) {
		t.Error("expected a throttled WARN for the first transient error")
	}
	firstWarnCount := len(handler.records)

	// Not memoized: a second call must retry the RPC, not short-circuit.
	if _, exact := c.Count(t.Context(), blocks, ref); exact {
		t.Error("exact = true on retry, want false")
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Errorf("CountTokens calls = %d, want 2 (transient error must not be memoized)", got)
	}

	// The WARN is throttled: the second consecutive error for the same
	// provider must not add a second WARN record.
	warnCount := 0
	for _, r := range handler.records[firstWarnCount:] {
		if r.Level == slog.LevelWarn {
			warnCount++
		}
	}
	if warnCount != 0 {
		t.Errorf("got %d additional WARN records on the throttled repeat, want 0", warnCount)
	}
}

func TestCounter_Count_canceledNotLoggedAsError(t *testing.T) {
	t.Parallel()
	logger, handler := testLogger()
	client := &fakeModelClient{
		countTokensFunc: func(context.Context, *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error) {
			return nil, canceledErr()
		},
	}
	lookup := newFakeLookup().with("anthropic", client)
	c := NewCounter(lookup, testProvider(t), logger)

	blocks := []*contentv1.ContentBlock{textBlock("1234")}
	ref := &modelv1.ModelRef{Provider: "anthropic", Id: "claude"}

	count, exact := c.Count(t.Context(), blocks, ref)
	if exact {
		t.Error("exact = true, want false")
	}
	if want := Fallback(blocks); count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
	if handler.hasLevel(slog.LevelError) {
		t.Error("cancellation must never be logged at ERROR")
	}
	if handler.hasLevel(slog.LevelWarn) {
		t.Error("cancellation must never be logged at WARN")
	}
}

func TestCounter_Count_concurrent(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger()
	client := &fakeModelClient{
		countTokensFunc: func(context.Context, *modelv1.CountTokensRequest) (*modelv1.CountTokensResponse, error) {
			return nil, unimplementedErr()
		},
	}
	lookup := newFakeLookup().with("anthropic", client)
	c := NewCounter(lookup, testProvider(t), logger)

	blocks := []*contentv1.ContentBlock{textBlock("hello")}
	ref := &modelv1.ModelRef{Provider: "anthropic", Id: "claude"}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			count, exact := c.Count(t.Context(), blocks, ref)
			if exact {
				t.Error("exact = true, want false")
			}
			if want := Fallback(blocks); count != want {
				t.Errorf("count = %d, want %d", count, want)
			}
		}()
	}
	wg.Wait()
}
