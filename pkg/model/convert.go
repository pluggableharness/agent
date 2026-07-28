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
	out := &modelv1.Capabilities{
		Models:              models,
		SlashCommands:       c.SlashCommands,
		ConfigSchema:        c.ConfigSchema,
		SupportedHookPoints: c.SupportedHookPoints,
		Auth:                authToProto(c.Auth),
		CatalogEtag:         c.CatalogEtag,
	}
	if c.CatalogFetchedAt != nil {
		out.CatalogFetchedAt = timestamppb.New(*c.CatalogFetchedAt)
	}
	return out
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
	out := &Capabilities{
		Models:              models,
		SlashCommands:       in.GetSlashCommands(),
		ConfigSchema:        in.GetConfigSchema(),
		SupportedHookPoints: in.GetSupportedHookPoints(),
		Auth:                authFromProto(in.GetAuth()),
		CatalogEtag:         in.CatalogEtag,
	}
	if ts := in.GetCatalogFetchedAt(); ts != nil {
		at := ts.AsTime()
		out.CatalogFetchedAt = &at
	}
	return out
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

		Catalog:                       catalogToProto(m.Catalog),
		MaxContextWindow:              m.MaxContextWindow,
		EffectiveContextWindowPercent: m.EffectiveContextWindowPercent,
		AutoCompactTokenLimit:         m.AutoCompactTokenLimit,
		Verbosity:                     verbosityToProto(m.Verbosity),
		ServiceTiers:                  append([]string(nil), m.ServiceTiers...),
		ApiBackend:                    m.APIBackend,
		TruncationPolicy:              m.TruncationPolicy,
		CompHash:                      m.CompHash,
	}
}

// catalogToProto converts optional picker metadata into the wire type.
func catalogToProto(c *CatalogMetadata) *modelv1.CatalogMetadata {
	if c == nil {
		return nil
	}
	return &modelv1.CatalogMetadata{
		DisplayName:    c.DisplayName,
		Description:    c.Description,
		Visible:        c.Visible,
		Priority:       c.Priority,
		SupportedInApi: c.SupportedInAPI,
		// Copied rather than aliased, for the same reason
		// thinkingSpecToProto copies Effort.Levels: a roster value is
		// typically shared across models.
		Aliases: append([]string(nil), c.Aliases...),
		Family:  c.Family,
	}
}

// catalogFromProto is catalogToProto's inverse.
func catalogFromProto(in *modelv1.CatalogMetadata) *CatalogMetadata {
	if in == nil {
		return nil
	}
	return &CatalogMetadata{
		DisplayName:    in.DisplayName,
		Description:    in.Description,
		Visible:        in.Visible,
		Priority:       in.Priority,
		SupportedInAPI: in.SupportedInApi,
		Aliases:        append([]string(nil), in.GetAliases()...),
		Family:         in.Family,
	}
}

// verbosityToProto converts an optional verbosity control into the wire type.
func verbosityToProto(v *VerbositySpec) *modelv1.VerbositySpec {
	if v == nil {
		return nil
	}
	return &modelv1.VerbositySpec{
		Supported: v.Supported,
		Levels:    append([]string(nil), v.Levels...),
		Default:   v.Default,
	}
}

// verbosityFromProto is verbosityToProto's inverse.
func verbosityFromProto(in *modelv1.VerbositySpec) *VerbositySpec {
	if in == nil {
		return nil
	}
	return &VerbositySpec{
		Supported: in.GetSupported(),
		Levels:    append([]string(nil), in.GetLevels()...),
		Default:   in.Default,
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

		Catalog:                       catalogFromProto(in.GetCatalog()),
		MaxContextWindow:              in.MaxContextWindow,
		EffectiveContextWindowPercent: in.EffectiveContextWindowPercent,
		AutoCompactTokenLimit:         in.AutoCompactTokenLimit,
		Verbosity:                     verbosityFromProto(in.GetVerbosity()),
		ServiceTiers:                  append([]string(nil), in.GetServiceTiers()...),
		APIBackend:                    in.ApiBackend,
		TruncationPolicy:              in.TruncationPolicy,
		CompHash:                      in.CompHash,
	}
}

// thinkingSpecToProto converts t into the generated wire type.
func thinkingSpecToProto(t ThinkingSpec) *modelv1.ThinkingSpec {
	out := &modelv1.ThinkingSpec{
		Supported:                t.Supported,
		AdaptiveByDefault:        t.AdaptiveByDefault,
		Disable:                  t.Disable,
		SupportsReasoningSummary: t.SupportsReasoningSummary,
		DefaultReasoningSummary:  t.DefaultReasoningSummary,
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
		Supported:                in.GetSupported(),
		AdaptiveByDefault:        in.GetAdaptiveByDefault(),
		Disable:                  in.GetDisable(),
		SupportsReasoningSummary: in.SupportsReasoningSummary,
		DefaultReasoningSummary:  in.DefaultReasoningSummary,
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
		Currency:   p.Currency,
		Free:       p.Free,
		Tiers:      tiers,
		SourceUnit: p.SourceUnit,
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
		Currency:   in.GetCurrency(),
		Free:       in.GetFree(),
		Tiers:      tiers,
		SourceUnit: in.SourceUnit,
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
	out.ImageInputPerMtok = t.ImageInputPerMtok
	out.AudioInputPerMtok = t.AudioInputPerMtok
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
		ImageInputPerMtok:  in.ImageInputPerMtok,
		AudioInputPerMtok:  in.AudioInputPerMtok,
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
		InputTokens:             u.InputTokens,
		OutputTokens:            u.OutputTokens,
		CacheReadTokens:         u.CacheReadTokens,
		CacheWriteTokens:        u.CacheWriteTokens,
		ReasoningTokens:         u.ReasoningTokens,
		RateLimits:              rateLimitsToProto(u.RateLimits),
		VendorCost:              vendorCostToProto(u.VendorCost),
		VendorTotalTokens:       u.VendorTotalTokens,
		Components:              componentsToProto(u.Components),
		ReasoningAlreadyCounted: u.ReasoningAlreadyCounted,
	}
}

// vendorCostToProto converts an optional vendor cost into the wire type.
func vendorCostToProto(c *VendorCost) *modelv1.VendorCost {
	if c == nil {
		return nil
	}
	return &modelv1.VendorCost{Amount: c.Amount, Unit: c.Unit, Currency: c.Currency}
}

// vendorCostFromProto is vendorCostToProto's inverse.
func vendorCostFromProto(in *modelv1.VendorCost) *VendorCost {
	if in == nil {
		return nil
	}
	return &VendorCost{Amount: in.GetAmount(), Unit: in.GetUnit(), Currency: in.Currency}
}

// componentsToProto converts vendor-defined counters into wire types.
func componentsToProto(in []UsageComponent) []*modelv1.UsageComponent {
	if len(in) == 0 {
		return nil
	}
	out := make([]*modelv1.UsageComponent, len(in))
	for i, c := range in {
		out[i] = &modelv1.UsageComponent{Name: c.Name, Value: c.Value}
	}
	return out
}

// componentsFromProto is componentsToProto's inverse.
func componentsFromProto(in []*modelv1.UsageComponent) []UsageComponent {
	if len(in) == 0 {
		return nil
	}
	out := make([]UsageComponent, len(in))
	for i, c := range in {
		out[i] = UsageComponent{Name: c.GetName(), Value: c.GetValue()}
	}
	return out
}

// rateLimitsToProto converts each snapshot into the generated wire type.
func rateLimitsToProto(in []RateLimitSnapshot) []*modelv1.RateLimitSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]*modelv1.RateLimitSnapshot, len(in))
	for i, r := range in {
		snap := &modelv1.RateLimitSnapshot{
			Kind:          r.Kind,
			Remaining:     r.Remaining,
			Limit:         r.Limit,
			LimitId:       r.LimitID,
			LimitName:     r.LimitName,
			WindowRole:    r.WindowRole,
			UsedPercent:   r.UsedPercent,
			WindowSeconds: r.WindowSeconds,
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
			Kind:          r.GetKind(),
			Remaining:     r.Remaining,
			Limit:         r.Limit,
			LimitID:       r.LimitId,
			LimitName:     r.LimitName,
			WindowRole:    r.GetWindowRole(),
			UsedPercent:   r.UsedPercent,
			WindowSeconds: r.WindowSeconds,
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
		InputTokens:             in.GetInputTokens(),
		OutputTokens:            in.GetOutputTokens(),
		CacheReadTokens:         in.CacheReadTokens,
		CacheWriteTokens:        in.CacheWriteTokens,
		ReasoningTokens:         in.ReasoningTokens,
		RateLimits:              rateLimitsFromProto(in.GetRateLimits()),
		VendorCost:              vendorCostFromProto(in.GetVendorCost()),
		VendorTotalTokens:       in.VendorTotalTokens,
		Components:              componentsFromProto(in.GetComponents()),
		ReasoningAlreadyCounted: in.ReasoningAlreadyCounted,
	}
}

// accountToProto converts an account snapshot into the wire type.
func accountToProto(a AccountSnapshot) *modelv1.AccountSnapshot {
	out := &modelv1.AccountSnapshot{
		Method:   a.Method,
		Metering: a.Metering,
		Plan:     a.Plan,
		Labels:   a.Labels,
		Quotas:   rateLimitsToProto(a.Quotas),
	}
	if a.FetchedAt != nil {
		out.FetchedAt = timestamppb.New(*a.FetchedAt)
	}
	return out
}

// accountFromProto is accountToProto's inverse.
func accountFromProto(in *modelv1.AccountSnapshot) AccountSnapshot {
	if in == nil {
		return AccountSnapshot{}
	}
	out := AccountSnapshot{
		Method:   in.GetMethod(),
		Metering: in.GetMetering(),
		Plan:     in.Plan,
		Labels:   in.GetLabels(),
		Quotas:   rateLimitsFromProto(in.GetQuotas()),
	}
	if ts := in.GetFetchedAt(); ts != nil {
		at := ts.AsTime()
		out.FetchedAt = &at
	}
	return out
}

// authToProto converts an optional auth descriptor into the wire type.
func authToProto(a *AuthDescriptor) *modelv1.AuthDescriptor {
	if a == nil {
		return nil
	}
	return &modelv1.AuthDescriptor{
		Method:   a.Method,
		Metering: a.Metering,
		Plan:     a.Plan,
		Labels:   a.Labels,
	}
}

// authFromProto is authToProto's inverse.
func authFromProto(in *modelv1.AuthDescriptor) *AuthDescriptor {
	if in == nil {
		return nil
	}
	return &AuthDescriptor{
		Method:   in.GetMethod(),
		Metering: in.GetMetering(),
		Plan:     in.Plan,
		Labels:   in.GetLabels(),
	}
}
