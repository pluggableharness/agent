package model_test

import (
	"testing"
	"time"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	model "github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func TestConvert_ModelSpecRoundTrip(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	writePerMtok := 3.75
	readPerMtok := 0.3
	tokensFrom := int64(0)
	tokensUntil := int64(200000)

	spec := model.Spec{
		ID:                        "claude-test",
		ContextWindow:             200000,
		MaxOutputTokens:           8192,
		SupportsToolUse:           true,
		SupportsVision:            true,
		SupportsStreaming:         true,
		SupportsParallelToolCalls: true,
		Thinking: model.ThinkingSpec{
			Supported:    true,
			Mode:         modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT,
			EffortLevels: []string{"low", "medium", "high"},
			CanDisable:   true,
			Default:      "medium",
		},
		Caching: model.CachingSpec{
			Supported:          true,
			Mode:               modelv1.CachingMode_CACHING_MODE_EXPLICIT_MARKERS,
			KeepaliveSupported: true,
		},
		Pricing: model.Pricing{
			Currency: "USD",
			Tiers: []model.PricingTier{
				{
					EffectiveFrom:     &from,
					EffectiveUntil:    &until,
					InputPerMtok:      3,
					OutputPerMtok:     15,
					CacheWritePerMtok: &writePerMtok,
					CacheReadPerMtok:  &readPerMtok,
					InputTokensFrom:   &tokensFrom,
					InputTokensUntil:  &tokensUntil,
				},
			},
		},
		SupportedToolChoiceModes: []modelv1.ToolChoiceMode{
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO,
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC,
		},
		SupportsDocuments: true,
	}

	wire := model.ModelSpecToProtoForTest(spec)
	back := model.ModelSpecFromProtoForTest(wire)

	if back.ID != spec.ID {
		t.Errorf("ID = %q, want %q", back.ID, spec.ID)
	}
	if back.ContextWindow != spec.ContextWindow {
		t.Errorf("ContextWindow = %d, want %d", back.ContextWindow, spec.ContextWindow)
	}
	if !back.SupportsParallelToolCalls {
		t.Errorf("SupportsParallelToolCalls = false, want true")
	}
	if back.Thinking.Default != "medium" {
		t.Errorf("Thinking.Default = %q, want %q", back.Thinking.Default, "medium")
	}
	if len(back.Thinking.EffortLevels) != 3 {
		t.Errorf("len(Thinking.EffortLevels) = %d, want 3", len(back.Thinking.EffortLevels))
	}
	if !back.Caching.Supported || back.Caching.Mode != modelv1.CachingMode_CACHING_MODE_EXPLICIT_MARKERS {
		t.Errorf("Caching = %+v, want supported explicit_markers", back.Caching)
	}
	if len(back.Pricing.Tiers) != 1 {
		t.Fatalf("len(Pricing.Tiers) = %d, want 1", len(back.Pricing.Tiers))
	}
	tier := back.Pricing.Tiers[0]
	if tier.CacheWritePerMtok == nil || *tier.CacheWritePerMtok != writePerMtok {
		t.Errorf("Tiers[0].CacheWritePerMtok = %v, want %v", tier.CacheWritePerMtok, writePerMtok)
	}
	if tier.EffectiveFrom == nil || !tier.EffectiveFrom.Equal(from) {
		t.Errorf("Tiers[0].EffectiveFrom = %v, want %v", tier.EffectiveFrom, from)
	}
	if tier.EffectiveUntil == nil || !tier.EffectiveUntil.Equal(until) {
		t.Errorf("Tiers[0].EffectiveUntil = %v, want %v", tier.EffectiveUntil, until)
	}
	if tier.InputTokensFrom == nil || *tier.InputTokensFrom != tokensFrom {
		t.Errorf("Tiers[0].InputTokensFrom = %v, want %v", tier.InputTokensFrom, tokensFrom)
	}
	if len(back.SupportedToolChoiceModes) != 2 {
		t.Errorf("len(SupportedToolChoiceModes) = %d, want 2", len(back.SupportedToolChoiceModes))
	}
	if !back.SupportsDocuments {
		t.Errorf("SupportsDocuments = false, want true")
	}
}

func TestConvert_ModelSpecFromProtoNil(t *testing.T) {
	t.Parallel()

	got := model.ModelSpecFromProtoForTest(nil)
	if got.ID != "" {
		t.Errorf("ModelSpecFromProtoForTest(nil).ID = %q, want empty", got.ID)
	}
}

func TestConvert_ThinkingSpecNoBudgetRange(t *testing.T) {
	t.Parallel()

	in := model.ThinkingSpec{Mode: modelv1.ThinkingMode_THINKING_MODE_NONE}
	wire := model.ThinkingSpecToProtoForTest(in)
	if wire.GetBudgetRange() != nil {
		t.Errorf("BudgetRange = %v, want nil", wire.GetBudgetRange())
	}
	back := model.ThinkingSpecFromProtoForTest(wire)
	if back.BudgetRange != nil {
		t.Errorf("round-tripped BudgetRange = %v, want nil", back.BudgetRange)
	}

	inBudget := model.ThinkingSpec{
		Supported:   true,
		Mode:        modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET,
		BudgetRange: &model.ThinkingBudgetRange{Min: 1024, Max: 32000},
		Default:     "4096",
	}
	wireBudget := model.ThinkingSpecToProtoForTest(inBudget)
	if wireBudget.GetBudgetRange().GetMin() != 1024 || wireBudget.GetBudgetRange().GetMax() != 32000 {
		t.Errorf("BudgetRange = %+v, want {1024 32000}", wireBudget.GetBudgetRange())
	}
	backBudget := model.ThinkingSpecFromProtoForTest(wireBudget)
	if backBudget.BudgetRange == nil || backBudget.BudgetRange.Min != 1024 || backBudget.BudgetRange.Max != 32000 {
		t.Errorf("round-tripped BudgetRange = %+v, want {1024 32000}", backBudget.BudgetRange)
	}
}

func TestConvert_ThinkingSpecFromProtoNil(t *testing.T) {
	t.Parallel()

	got := model.ThinkingSpecFromProtoForTest(nil)
	if got.Supported {
		t.Errorf("Supported = true, want false for nil input")
	}
}

func TestConvert_CachingSpecFromProtoNil(t *testing.T) {
	t.Parallel()

	got := model.CachingSpecFromProtoForTest(nil)
	if got.Supported {
		t.Errorf("Supported = true, want false for nil input")
	}
}

func TestConvert_PricingRoundTrip_Free(t *testing.T) {
	t.Parallel()

	p := model.Pricing{Currency: "USD", Free: true}
	wire := model.PricingToProtoForTest(p)
	if !wire.GetFree() {
		t.Errorf("Free = false, want true")
	}
	back := model.PricingFromProtoForTest(wire)
	if !back.Free || len(back.Tiers) != 0 {
		t.Errorf("round-tripped Pricing = %+v, want free with no tiers", back)
	}
}

func TestConvert_PricingFromProtoNil(t *testing.T) {
	t.Parallel()

	got := model.PricingFromProtoForTest(nil)
	if got.Currency != "" || got.Tiers != nil {
		t.Errorf("PricingFromProtoForTest(nil) = %+v, want zero value", got)
	}
}

func TestConvert_PricingTierFromProtoNil(t *testing.T) {
	t.Parallel()

	got := model.PricingTierFromProtoForTest(nil)
	if got.InputPerMtok != 0 {
		t.Errorf("PricingTierFromProtoForTest(nil).InputPerMtok = %v, want 0", got.InputPerMtok)
	}
}

func TestConvert_UsageRoundTrip(t *testing.T) {
	t.Parallel()

	cacheRead := int64(100)
	cacheWrite := int64(50)
	reasoning := int64(25)
	u := model.Usage{
		InputTokens:      1000,
		OutputTokens:     500,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
		ReasoningTokens:  &reasoning,
	}
	wire := model.UsageToProtoForTest(u)
	back := model.UsageFromProtoForTest(wire)

	if back.InputTokens != u.InputTokens || back.OutputTokens != u.OutputTokens {
		t.Errorf("round-tripped token counts = %+v, want %+v", back, u)
	}
	if back.CacheReadTokens == nil || *back.CacheReadTokens != cacheRead {
		t.Errorf("CacheReadTokens = %v, want %v", back.CacheReadTokens, cacheRead)
	}
	if back.ReasoningTokens == nil || *back.ReasoningTokens != reasoning {
		t.Errorf("ReasoningTokens = %v, want %v", back.ReasoningTokens, reasoning)
	}
}

func TestConvert_UsageFromProtoNil(t *testing.T) {
	t.Parallel()

	got := model.UsageFromProtoForTest(nil)
	if got.InputTokens != 0 || got.CacheReadTokens != nil {
		t.Errorf("UsageFromProtoForTest(nil) = %+v, want zero value", got)
	}
}

func TestConvert_CapabilitiesRoundTrip(t *testing.T) {
	t.Parallel()

	caps := &model.Capabilities{
		Models: []model.Spec{{
			ID:       "claude-test",
			Thinking: model.ThinkingSpec{Mode: modelv1.ThinkingMode_THINKING_MODE_NONE},
			Caching:  model.CachingSpec{Mode: modelv1.CachingMode_CACHING_MODE_NONE},
			Pricing:  model.Pricing{Currency: "USD", Free: true},
		}},
		ConfigSchema: &configv1.ConfigSchema{},
	}

	wire := model.CapabilitiesToProtoForTest(caps)
	if len(wire.GetModels()) != 1 {
		t.Fatalf("len(wire.Models) = %d, want 1", len(wire.GetModels()))
	}
	back := model.CapabilitiesFromProtoForTest(wire)
	if len(back.Models) != 1 || back.Models[0].ID != "claude-test" {
		t.Errorf("round-tripped Capabilities = %+v, want one model claude-test", back)
	}
}

func TestConvert_CapabilitiesFromProtoNil(t *testing.T) {
	t.Parallel()

	if got := model.CapabilitiesFromProtoForTest(nil); got != nil {
		t.Errorf("CapabilitiesFromProtoForTest(nil) = %+v, want nil", got)
	}
}

func TestConvert_ModelErrorRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *model.Error
	}{
		{
			name: "full detail",
			err: &model.Error{
				Category:   modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED,
				Message:    "rate limited",
				Retryable:  true,
				RetryAfter: 30 * time.Second,
				RawDetail:  "429 too many requests",
			},
		},
		{
			name: "minimal",
			err: &model.Error{
				Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
				Message:  "bad request",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wire := model.ModelErrorToProtoForTest(tt.err)
			back := model.ModelErrorFromProtoForTest(wire)

			if back.Category != tt.err.Category {
				t.Errorf("Category = %v, want %v", back.Category, tt.err.Category)
			}
			if back.Message != tt.err.Message {
				t.Errorf("Message = %q, want %q", back.Message, tt.err.Message)
			}
			if back.Retryable != tt.err.Retryable {
				t.Errorf("Retryable = %v, want %v", back.Retryable, tt.err.Retryable)
			}
			if back.RetryAfter != tt.err.RetryAfter {
				t.Errorf("RetryAfter = %v, want %v", back.RetryAfter, tt.err.RetryAfter)
			}
			if back.RawDetail != tt.err.RawDetail {
				t.Errorf("RawDetail = %q, want %q", back.RawDetail, tt.err.RawDetail)
			}
		})
	}
}

func TestConvert_ModelErrorFromProtoNil(t *testing.T) {
	t.Parallel()

	if got := model.ModelErrorFromProtoForTest(nil); got != nil {
		t.Errorf("ModelErrorFromProtoForTest(nil) = %+v, want nil", got)
	}
}
