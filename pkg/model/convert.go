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
		Supported:    t.Supported,
		Mode:         t.Mode,
		EffortLevels: t.EffortLevels,
		CanDisable:   t.CanDisable,
	}
	if t.BudgetRange != nil {
		out.BudgetRange = &modelv1.ThinkingBudgetRange{
			Min: t.BudgetRange.Min,
			Max: t.BudgetRange.Max,
		}
	}
	if t.Default != "" {
		def := t.Default
		out.Default = &def
	}
	return out
}

// thinkingSpecFromProto is thinkingSpecToProto's inverse.
func thinkingSpecFromProto(in *modelv1.ThinkingSpec) ThinkingSpec {
	if in == nil {
		return ThinkingSpec{}
	}
	out := ThinkingSpec{
		Supported:    in.GetSupported(),
		Mode:         in.GetMode(),
		EffortLevels: in.GetEffortLevels(),
		CanDisable:   in.GetCanDisable(),
		Default:      in.GetDefault(),
	}
	if br := in.GetBudgetRange(); br != nil {
		out.BudgetRange = &ThinkingBudgetRange{Min: br.GetMin(), Max: br.GetMax()}
	}
	return out
}

// cachingSpecToProto converts c into the generated wire type.
func cachingSpecToProto(c CachingSpec) *modelv1.CachingSpec {
	return &modelv1.CachingSpec{
		Supported:          c.Supported,
		Mode:               c.Mode,
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
		Mode:               in.GetMode(),
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
	}
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
	}
}
