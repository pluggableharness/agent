package model

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// capabilitiesToProto converts c into the generated wire type
// GetCapabilities returns. c is assumed already validated (NewCapabilities
// is the only exported constructor).
func capabilitiesToProto(c *Capabilities) *modelv1.Capabilities {
	models := make([]*modelv1.ModelSpec, len(c.Models))
	for i, m := range c.Models {
		models[i] = modelSpecToProto(m)
	}
	return &modelv1.Capabilities{
		Models:              models,
		SlashCommands:       c.SlashCommands,
		ConfigSchema:        c.ConfigSchema,
		SupportedHookPoints: c.SupportedHookPoints,
	}
}

// capabilitiesFromProto is capabilitiesToProto's inverse.
func capabilitiesFromProto(in *modelv1.Capabilities) *Capabilities {
	if in == nil {
		return nil
	}
	models := make([]Spec, len(in.GetModels()))
	for i, m := range in.GetModels() {
		models[i] = modelSpecFromProto(m)
	}
	return &Capabilities{
		Models:              models,
		SlashCommands:       in.GetSlashCommands(),
		ConfigSchema:        in.GetConfigSchema(),
		SupportedHookPoints: in.GetSupportedHookPoints(),
	}
}

// modelSpecToProto converts m into the generated wire type.
func modelSpecToProto(m Spec) *modelv1.ModelSpec {
	supportsParallel := m.SupportsParallelToolCalls
	return &modelv1.ModelSpec{
		Id:                        m.ID,
		ContextWindow:             m.ContextWindow,
		MaxOutputTokens:           m.MaxOutputTokens,
		SupportsToolUse:           m.SupportsToolUse,
		SupportsVision:            m.SupportsVision,
		SupportsStreaming:         m.SupportsStreaming,
		SupportsParallelToolCalls: &supportsParallel,
		Thinking:                  thinkingSpecToProto(m.Thinking),
		Caching:                   cachingSpecToProto(m.Caching),
		Pricing:                   pricingToProto(m.Pricing),
		SupportedToolChoiceModes:  m.SupportedToolChoiceModes,
		SupportsDocuments:         m.SupportsDocuments,
	}
}

// modelSpecFromProto is modelSpecToProto's inverse.
func modelSpecFromProto(in *modelv1.ModelSpec) Spec {
	if in == nil {
		return Spec{}
	}
	return Spec{
		ID:                        in.GetId(),
		ContextWindow:             in.GetContextWindow(),
		MaxOutputTokens:           in.GetMaxOutputTokens(),
		SupportsToolUse:           in.GetSupportsToolUse(),
		SupportsVision:            in.GetSupportsVision(),
		SupportsStreaming:         in.GetSupportsStreaming(),
		SupportsParallelToolCalls: in.GetSupportsParallelToolCalls(),
		Thinking:                  thinkingSpecFromProto(in.GetThinking()),
		Caching:                   cachingSpecFromProto(in.GetCaching()),
		Pricing:                   pricingFromProto(in.GetPricing()),
		SupportedToolChoiceModes:  in.GetSupportedToolChoiceModes(),
		SupportsDocuments:         in.GetSupportsDocuments(),
	}
}

// thinkingSpecToProto converts t into the generated wire type.
func thinkingSpecToProto(t ThinkingSpec) *modelv1.ThinkingSpec {
	out := &modelv1.ThinkingSpec{
		Supported:         t.Supported,
		AdaptiveByDefault: t.AdaptiveByDefault,
		Disable:           t.Disable,
	}
	if t.Effort != nil {
		out.Effort = &modelv1.EffortControl{
			// Copied rather than aliased: a Spec's Levels slice is
			// typically a package-level roster value shared across models,
			// and handing it to the wire type would let a later mutation
			// reach every model that shares it.
			Levels:  append([]string(nil), t.Effort.Levels...),
			Default: t.Effort.Default,
		}
	}
	if t.Budget != nil {
		budget := &modelv1.BudgetControl{
			Range: &modelv1.ThinkingBudgetRange{
				Min: t.Budget.Range.Min,
				Max: t.Budget.Range.Max,
			},
			Deprecated: t.Budget.Deprecated,
		}
		if t.Budget.Default != nil {
			def := *t.Budget.Default
			budget.Default = &def
		}
		out.Budget = budget
	}
	return out
}

// thinkingSpecFromProto is thinkingSpecToProto's inverse.
func thinkingSpecFromProto(in *modelv1.ThinkingSpec) ThinkingSpec {
	if in == nil {
		return ThinkingSpec{}
	}
	out := ThinkingSpec{
		Supported:         in.GetSupported(),
		AdaptiveByDefault: in.GetAdaptiveByDefault(),
		Disable:           in.GetDisable(),
	}
	if e := in.GetEffort(); e != nil {
		out.Effort = &EffortControl{
			Levels:  append([]string(nil), e.GetLevels()...),
			Default: e.GetDefault(),
		}
	}
	if b := in.GetBudget(); b != nil {
		budget := &BudgetControl{
			Range:      ThinkingBudgetRange{Min: b.GetRange().GetMin(), Max: b.GetRange().GetMax()},
			Deprecated: b.GetDeprecated(),
		}
		if b.Default != nil {
			def := b.GetDefault()
			budget.Default = &def
		}
		out.Budget = budget
	}
	return out
}

// cachingSpecToProto converts c into the generated wire type.
func cachingSpecToProto(c CachingSpec) *modelv1.CachingSpec {
	return &modelv1.CachingSpec{
		Supported:          c.Supported,
		ExplicitMarkers:    c.ExplicitMarkers,
		ImplicitAutomatic:  c.ImplicitAutomatic,
		KeepaliveSupported: c.KeepaliveSupported,
	}
}

// cachingSpecFromProto is cachingSpecToProto's inverse.
func cachingSpecFromProto(in *modelv1.CachingSpec) CachingSpec {
	if in == nil {
		return CachingSpec{}
	}
	return CachingSpec{
		Supported:          in.GetSupported(),
		ExplicitMarkers:    in.GetExplicitMarkers(),
		ImplicitAutomatic:  in.GetImplicitAutomatic(),
		KeepaliveSupported: in.GetKeepaliveSupported(),
	}
}

// pricingToProto converts p into the generated wire type.
func pricingToProto(p Pricing) *modelv1.Pricing {
	tiers := make([]*modelv1.PricingTier, len(p.Tiers))
	for i, t := range p.Tiers {
		tiers[i] = pricingTierToProto(t)
	}
	return &modelv1.Pricing{
		Currency: p.Currency,
		Free:     p.Free,
		Tiers:    tiers,
	}
}

// pricingFromProto is pricingToProto's inverse.
func pricingFromProto(in *modelv1.Pricing) Pricing {
	if in == nil {
		return Pricing{}
	}
	tiers := make([]PricingTier, len(in.GetTiers()))
	for i, t := range in.GetTiers() {
		tiers[i] = pricingTierFromProto(t)
	}
	return Pricing{
		Currency: in.GetCurrency(),
		Free:     in.GetFree(),
		Tiers:    tiers,
	}
}

// pricingTierToProto converts t into the generated wire type.
func pricingTierToProto(t PricingTier) *modelv1.PricingTier {
	out := &modelv1.PricingTier{
		InputPerMtok:  t.InputPerMtok,
		OutputPerMtok: t.OutputPerMtok,
	}
	if t.EffectiveFrom != nil {
		out.EffectiveFrom = timestamppb.New(*t.EffectiveFrom)
	}
	if t.EffectiveUntil != nil {
		out.EffectiveUntil = timestamppb.New(*t.EffectiveUntil)
	}
	out.CacheWritePerMtok = t.CacheWritePerMtok
	out.CacheReadPerMtok = t.CacheReadPerMtok
	out.BatchInputPerMtok = t.BatchInputPerMtok
	out.BatchOutputPerMtok = t.BatchOutputPerMtok
	out.InputTokensFrom = t.InputTokensFrom
	out.InputTokensUntil = t.InputTokensUntil
	return out
}

// pricingTierFromProto is pricingTierToProto's inverse.
func pricingTierFromProto(in *modelv1.PricingTier) PricingTier {
	if in == nil {
		return PricingTier{}
	}
	out := PricingTier{
		InputPerMtok:       in.GetInputPerMtok(),
		OutputPerMtok:      in.GetOutputPerMtok(),
		CacheWritePerMtok:  in.CacheWritePerMtok,
		CacheReadPerMtok:   in.CacheReadPerMtok,
		BatchInputPerMtok:  in.BatchInputPerMtok,
		BatchOutputPerMtok: in.BatchOutputPerMtok,
		InputTokensFrom:    in.InputTokensFrom,
		InputTokensUntil:   in.InputTokensUntil,
	}
	if ef := in.GetEffectiveFrom(); ef != nil {
		t := ef.AsTime()
		out.EffectiveFrom = &t
	}
	if eu := in.GetEffectiveUntil(); eu != nil {
		t := eu.AsTime()
		out.EffectiveUntil = &t
	}
	return out
}

// usageToProto converts u into the generated wire type carried by a
// StreamEvent Usage variant.
func usageToProto(u Usage) *modelv1.Usage {
	return &modelv1.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		ReasoningTokens:  u.ReasoningTokens,
		RateLimits:       rateLimitsToProto(u.RateLimits),
	}
}

// rateLimitsToProto converts each snapshot into the generated wire type.
func rateLimitsToProto(in []RateLimitSnapshot) []*modelv1.RateLimitSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]*modelv1.RateLimitSnapshot, len(in))
	for i, r := range in {
		snap := &modelv1.RateLimitSnapshot{
			Kind:      r.Kind,
			Remaining: r.Remaining,
			Limit:     r.Limit,
		}
		if r.ResetAt != nil {
			snap.ResetAt = timestamppb.New(*r.ResetAt)
		}
		out[i] = snap
	}
	return out
}

// rateLimitsFromProto is rateLimitsToProto's inverse.
func rateLimitsFromProto(in []*modelv1.RateLimitSnapshot) []RateLimitSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]RateLimitSnapshot, len(in))
	for i, r := range in {
		snap := RateLimitSnapshot{
			Kind:      r.GetKind(),
			Remaining: r.Remaining,
			Limit:     r.Limit,
		}
		if ts := r.GetResetAt(); ts != nil {
			at := ts.AsTime()
			snap.ResetAt = &at
		}
		out[i] = snap
	}
	return out
}

// usageFromProto is usageToProto's inverse.
func usageFromProto(in *modelv1.Usage) Usage {
	if in == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:      in.GetInputTokens(),
		OutputTokens:     in.GetOutputTokens(),
		CacheReadTokens:  in.CacheReadTokens,
		CacheWriteTokens: in.CacheWriteTokens,
		ReasoningTokens:  in.ReasoningTokens,
		RateLimits:       rateLimitsFromProto(in.GetRateLimits()),
	}
}
