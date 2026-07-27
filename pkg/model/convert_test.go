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
			Supported: true,
			Effort: &model.EffortControl{
				Levels:  []string{"low", "medium", "high"},
				Default: "medium",
			},
			AdaptiveByDefault: true,
			Disable:           modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_CONDITIONAL,
		},
		Caching: model.CachingSpec{
			Supported:          true,
			ExplicitMarkers:    true,
			ImplicitAutomatic:  true,
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
	if back.Thinking.Effort == nil {
		t.Fatal("Thinking.Effort = nil, want the declared effort control")
	}
	if back.Thinking.Effort.Default != "medium" {
		t.Errorf("Thinking.Effort.Default = %q, want %q", back.Thinking.Effort.Default, "medium")
	}
	if len(back.Thinking.Effort.Levels) != 3 {
		t.Errorf("len(Thinking.Effort.Levels) = %d, want 3", len(back.Thinking.Effort.Levels))
	}
	// The two axes are independent, so a spec declaring an effort ladder
	// AND adaptive-by-default must round-trip both — the exact pair the
	// earlier single-mode shape could not carry.
	if !back.Thinking.AdaptiveByDefault {
		t.Error("Thinking.AdaptiveByDefault = false, want true")
	}
	if back.Thinking.Disable != modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_CONDITIONAL {
		t.Errorf("Thinking.Disable = %v, want CONDITIONAL", back.Thinking.Disable)
	}
	// A model declaring BOTH caching axes must round-trip both — the pair
	// the earlier single-mode enum could not carry.
	if !back.Caching.Supported || !back.Caching.ExplicitMarkers || !back.Caching.ImplicitAutomatic {
		t.Errorf("Caching = %+v, want supported with both axes true", back.Caching)
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

func TestConvert_ThinkingSpecBudgetControl(t *testing.T) {
	t.Parallel()

	// A model with no thinking at all carries neither control.
	wire := model.ThinkingSpecToProtoForTest(model.ThinkingSpec{})
	if wire.GetBudget() != nil {
		t.Errorf("Budget = %v, want nil", wire.GetBudget())
	}
	if wire.GetEffort() != nil {
		t.Errorf("Effort = %v, want nil", wire.GetEffort())
	}
	if back := model.ThinkingSpecFromProtoForTest(wire); back.Budget != nil || back.Effort != nil {
		t.Errorf("round-tripped controls = (%v, %v), want both nil", back.Effort, back.Budget)
	}

	def := int64(4096)
	inBudget := model.ThinkingSpec{
		Supported: true,
		Budget: &model.BudgetControl{
			Range:      model.ThinkingBudgetRange{Min: 1024, Max: 32000},
			Default:    &def,
			Deprecated: true,
		},
		Disable: modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
	}
	wireBudget := model.ThinkingSpecToProtoForTest(inBudget)
	if got := wireBudget.GetBudget().GetRange(); got.GetMin() != 1024 || got.GetMax() != 32000 {
		t.Errorf("Budget.Range = %+v, want {1024 32000}", got)
	}
	if !wireBudget.GetBudget().GetDeprecated() {
		t.Error("Budget.Deprecated = false, want true")
	}

	backBudget := model.ThinkingSpecFromProtoForTest(wireBudget)
	if backBudget.Budget == nil {
		t.Fatal("round-tripped Budget = nil, want the declared control")
	}
	if backBudget.Budget.Range.Min != 1024 || backBudget.Budget.Range.Max != 32000 {
		t.Errorf("round-tripped Budget.Range = %+v, want {1024 32000}", backBudget.Budget.Range)
	}
	if backBudget.Budget.Default == nil || *backBudget.Budget.Default != def {
		t.Errorf("round-tripped Budget.Default = %v, want %d", backBudget.Budget.Default, def)
	}
	if !backBudget.Budget.Deprecated {
		t.Error("round-tripped Budget.Deprecated = false, want true")
	}
}

func TestConvert_ThinkingSpecBudgetDefaultAbsentIsNotZero(t *testing.T) {
	t.Parallel()

	// An omitted default means "the vendor reasons zero tokens by
	// default", which is a different statement from "the vendor's default
	// budget is the number 0" — collapsing the two would lose the
	// distinction the pointer exists to carry.
	in := model.ThinkingSpec{
		Supported: true,
		Budget: &model.BudgetControl{
			Range: model.ThinkingBudgetRange{Min: 1024, Max: 32000},
		},
		Disable: modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
	}
	wire := model.ThinkingSpecToProtoForTest(in)
	if wire.GetBudget().Default != nil {
		t.Errorf("Budget.Default = %v, want nil", wire.GetBudget().Default)
	}
	if back := model.ThinkingSpecFromProtoForTest(wire); back.Budget.Default != nil {
		t.Errorf("round-tripped Budget.Default = %v, want nil", back.Budget.Default)
	}
}

func TestConvert_ThinkingSpecEffortLevelsAreCopied(t *testing.T) {
	t.Parallel()

	// A roster typically shares one levels slice across several models, so
	// aliasing it into the wire type would let a mutation through one
	// model's spec reach every other model that shares it.
	levels := []string{"low", "high"}
	in := model.ThinkingSpec{
		Supported: true,
		Effort:    &model.EffortControl{Levels: levels, Default: "low"},
		Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
	}
	wire := model.ThinkingSpecToProtoForTest(in)
	levels[0] = "mutated"

	if got := wire.GetEffort().GetLevels()[0]; got != "low" {
		t.Errorf("wire levels[0] = %q after mutating the source slice, want %q", got, "low")
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
			Thinking: model.ThinkingSpec{},
			Caching:  model.CachingSpec{},
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

func TestConvert_UsageRateLimitsRoundTrip(t *testing.T) {
	t.Parallel()

	reset := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	remaining := int64(2)
	limit := int64(1000)

	// Two budgets at once, which is the normal case rather than an edge:
	// vendors meter requests and tokens separately and they exhaust
	// independently, so a single snapshot could not say which one stopped
	// a session.
	in := model.Usage{
		InputTokens:  10,
		OutputTokens: 5,
		RateLimits: []model.RateLimitSnapshot{
			{
				Kind:      modelv1.RateLimitKind_RATE_LIMIT_KIND_REQUESTS,
				Remaining: &remaining,
				Limit:     &limit,
				ResetAt:   &reset,
			},
			// Only Kind set: a vendor that publishes the budget's existence
			// but no numbers still produces a useful snapshot, and the
			// adapter must not invent the missing values.
			{Kind: modelv1.RateLimitKind_RATE_LIMIT_KIND_OUTPUT_TOKENS},
		},
	}

	back := model.UsageFromProtoForTest(model.UsageToProtoForTest(in))

	if len(back.RateLimits) != 2 {
		t.Fatalf("len(RateLimits) = %d, want 2", len(back.RateLimits))
	}
	first := back.RateLimits[0]
	if first.Kind != modelv1.RateLimitKind_RATE_LIMIT_KIND_REQUESTS {
		t.Errorf("RateLimits[0].Kind = %v, want REQUESTS", first.Kind)
	}
	if first.Remaining == nil || *first.Remaining != remaining {
		t.Errorf("RateLimits[0].Remaining = %v, want %d", first.Remaining, remaining)
	}
	if first.ResetAt == nil || !first.ResetAt.Equal(reset) {
		t.Errorf("RateLimits[0].ResetAt = %v, want %v", first.ResetAt, reset)
	}

	second := back.RateLimits[1]
	if second.Kind != modelv1.RateLimitKind_RATE_LIMIT_KIND_OUTPUT_TOKENS {
		t.Errorf("RateLimits[1].Kind = %v, want OUTPUT_TOKENS", second.Kind)
	}
	if second.Remaining != nil || second.Limit != nil || second.ResetAt != nil {
		t.Errorf("RateLimits[1] = %+v, want every numeric field absent", second)
	}
}

func TestConvert_UsageWithoutRateLimitsStaysNil(t *testing.T) {
	t.Parallel()

	// A vendor publishing nothing must produce no snapshots at all, rather
	// than an empty-but-present one that would read as "the budget exists".
	back := model.UsageFromProtoForTest(model.UsageToProtoForTest(model.Usage{InputTokens: 1}))
	if back.RateLimits != nil {
		t.Errorf("RateLimits = %+v, want nil", back.RateLimits)
	}
}
