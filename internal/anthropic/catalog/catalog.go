package catalog

import (
	"time"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// sourcedOn is the date every figure in this file was transcribed from
// Anthropic's published documentation (platform.claude.com's models
// overview and pricing pages). It is a plain string rather than a
// time.Time because nothing computes with it — it exists so a reader can
// tell at a glance how stale the roster is, and so the staleness check in
// this package's CLAUDE.md has a single value to compare against.
const sourcedOn = "2026-07-25"

// currency is the only currency this catalog quotes.
// docs/specifications/model/data-types.md#pricing constrains v1 to "USD".
const currency = "USD"

// Token-count constants shared across several models, named so a reader
// sees the shape of the roster rather than a wall of digits.
const (
	contextWindow1M   = 1_000_000
	contextWindow200K = 200_000

	maxOutput128K = 128_000
	maxOutput64K  = 64_000
)

// effortLevels5 is the full effort ladder Anthropic exposes on Claude
// Opus 4.7 and later (xhigh was introduced with Opus 4.7, between high
// and max). effortLevels4 is the pre-4.7 ladder, still current for the
// 4.6 generation.
var (
	effortLevels5 = []string{"low", "medium", "high", "xhigh", "max"}
	effortLevels4 = []string{"low", "medium", "high", "max"}
)

// defaultEffort is the level Anthropic applies when a request omits
// output_config.effort entirely, for every model in this roster that
// exposes an effort ladder.
const defaultEffort = "high"

// sonnet5IntroEnd is the instant Claude Sonnet 5's introductory pricing
// stops applying. Anthropic states the intro rate holds "through
// August 31, 2026", so the first instant of the standard rate is
// 2026-09-01T00:00:00Z — the exclusive upper bound of the intro tier and
// the inclusive lower bound of the standard one, matching
// docs/specifications/model/data-types.md#pricing's half-open
// effective_from/effective_until convention.
//
// This pair of tiers is the one place in the roster where PricingTier's
// time dimension does real work: a session run before the cutover must
// replay showing the intro rate it actually paid, which is exactly why
// the kernel persists cost_usd at usage-event time rather than
// recomputing it later.
var sonnet5IntroEnd = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// Models returns the roster, freshly built on every call so a caller
// mutating a returned Spec (or the slices inside it) cannot corrupt the
// roster every later caller sees. Order is fixed and deterministic:
// newest generation first, then descending capability tier.
func Models() []model.Spec {
	return []model.Spec{
		fable5(),
		opus5(),
		opus48(),
		opus47(),
		opus46(),
		sonnet5(),
		sonnet46(),
		haiku45(),
	}
}

// base returns the capability fields every model in this roster shares.
// All eight accept text, images, PDFs, and tool declarations; all eight
// stream; all eight can return several tool_use blocks in one turn; all
// eight accept every tool_choice shape the protocol models; and all eight
// use Anthropic's explicit cache_control markers rather than automatic
// caching.
//
// Anthropic's per-model minimum cacheable prefix (512 tokens on Opus 5
// and Fable 5, 1024 on Opus 4.8 / Sonnet 5 / Sonnet 4.6, 2048 on
// Opus 4.7, 4096 on Opus 4.6 and Haiku 4.5) is deliberately not modeled:
// CachingSpec has no field for it, and a prefix below the threshold
// simply does not cache rather than erroring, so the kernel loses nothing
// by not knowing it.
func base(id string, contextWindow, maxOutput int64) model.Spec {
	return model.Spec{
		ID:                        id,
		ContextWindow:             contextWindow,
		MaxOutputTokens:           maxOutput,
		SupportsToolUse:           true,
		SupportsVision:            true,
		SupportsStreaming:         true,
		SupportsParallelToolCalls: true,
		SupportsDocuments:         true,
		Caching: model.CachingSpec{
			Supported:       true,
			ExplicitMarkers: true,
			// The plugin runs no background cache-keepalive loop. A
			// keepalive would mean issuing extra billed requests on the
			// operator's behalf without them asking, which is not a
			// decision a provider plugin should make silently.
			KeepaliveSupported: false,
		},
		SupportedToolChoiceModes: []modelv1.ToolChoiceMode{
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO,
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY,
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_NONE,
			modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC,
		},
	}
}

// effortThinking builds the ThinkingSpec for a model that reasons
// adaptively and exposes a named effort ladder (output_config.effort) on
// top of it. disable says whether, and when, that reasoning can be turned
// off — see fable5 and opus5 for the two models where the answer is not a
// plain yes.
//
// AdaptiveByDefault is true for every model built here: omitting thinking
// config entirely still reasons, and the adapter sends
// thinking:{type:"adaptive"} alongside the effort level rather than
// instead of it. Both facts are declarable now that ThinkingSpec's axes
// are independent; the earlier single-mode shape could only say one, and
// said the effort half.
//
// None of these models declares a BudgetControl. Anthropic removed
// budget_tokens outright on Opus 4.7 and later, and while the 4.6
// generation reportedly still honors it transitionally, this roster has
// never claimed that and adding the claim needs its own pass against the
// live docs — see this package's CLAUDE.md on never writing a capability
// here from memory.
//
// levels is copied rather than aliased: effortLevels4/effortLevels5 are
// package-level slices shared by several models, so handing one straight
// to a caller would let that caller's mutation reach every later Models()
// call, defeating the whole point of rebuilding the roster per call.
func effortThinking(levels []string, disable modelv1.ThinkingDisableSupport) model.ThinkingSpec {
	return model.ThinkingSpec{
		Supported: true,
		Effort: &model.EffortControl{
			Levels:  append([]string(nil), levels...),
			Default: defaultEffort,
		},
		AdaptiveByDefault: true,
		Disable:           disable,
	}
}

// flatPricing builds a single-tier Pricing with no time or input-size
// bounds — the shape every model here uses except Claude Sonnet 5, whose
// introductory window needs two tiers.
func flatPricing(input, output, cacheWrite, cacheRead, batchInput, batchOutput float64) model.Pricing {
	return model.Pricing{
		Currency: currency,
		Tiers: []model.PricingTier{{
			InputPerMtok:      input,
			OutputPerMtok:     output,
			CacheWritePerMtok: &cacheWrite,
			CacheReadPerMtok:  &cacheRead,
			BatchInputPerMtok: &batchInput,
			// Named rather than inlined so its address is stable — every
			// PricingTier rate that can be absent is a pointer, and taking
			// the address of a parameter is the least surprising way to
			// build one from a plain float.
			BatchOutputPerMtok: &batchOutput,
		}},
	}
}

// opusPricing is the rate card shared by Claude Opus 5, 4.8, 4.7, and
// 4.6: $5/$25 per MTok, 5-minute cache writes at 1.25x input, cache reads
// at 0.1x input, batch at 50% off both directions.
//
// Only the 5-minute cache-write rate is quoted. Anthropic also publishes
// a 1-hour cache-write rate at 2x input ($10/MTok here), but PricingTier
// has exactly one cache_write_per_mtok field and the kernel places every
// breakpoint without a ttl, so 5-minute is the rate this plugin can
// actually incur. Quoting the 1-hour rate would overstate every cached
// turn's cost by 60%.
func opusPricing() model.Pricing {
	return flatPricing(5.00, 25.00, 6.25, 0.50, 2.50, 12.50)
}

// fable5 is Claude Fable 5 — Anthropic's most capable widely released
// model.
//
// Its thinking is declared DISCRETE_EFFORT with can_disable false rather
// than ALWAYS_ON_ADAPTIVE, which is a deliberate choice between two modes
// that each capture half the truth. Fable 5's reasoning genuinely cannot
// be switched off (an explicit thinking:{type:"disabled"} is a 400), which
// is what ALWAYS_ON_ADAPTIVE describes — but it *does* expose the full
// output_config.effort ladder, and ALWAYS_ON_ADAPTIVE means "no
// caller-selectable effort level or budget", which would hide a control
// the kernel can legitimately use. DISCRETE_EFFORT plus can_disable:false
// carries both facts; the reverse choice carries only one.
//
// Claude Mythos 5 shares Fable 5's specs and pricing exactly but is
// invitation-only through Project Glasswing, so it is deliberately absent
// from this roster: advertising a model most operators cannot call would
// make it a routing candidate that fails at request time.
func fable5() model.Spec {
	s := base("claude-fable-5", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels5, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_NEVER)
	s.Pricing = flatPricing(10.00, 50.00, 12.50, 1.00, 5.00, 25.00)
	return s
}

// opus5 is Claude Opus 5, the current default for complex agentic coding.
//
// can_disable is true, but with a caveat the ThinkingSpec shape cannot
// express: thinking:{type:"disabled"} is accepted only at effort high or
// below, and returns a 400 paired with xhigh or max. The protocol has no
// field for a conditional disable, and declaring can_disable:false would
// be the larger lie — it would tell the kernel a control exists nowhere
// when it in fact exists across three of the five effort levels. The
// adapter does not attempt to reconcile the two: the kernel sends effort
// and the adapter forwards it, so this combination only arises if the
// kernel explicitly asks for both.
func opus5() model.Spec {
	s := base("claude-opus-5", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels5, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_CONDITIONAL)
	s.Pricing = opusPricing()
	return s
}

// opus48 is Claude Opus 4.8 — the previous Opus generation, still
// current and the recommended fallback target for an Opus 5 refusal.
func opus48() model.Spec {
	s := base("claude-opus-4-8", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels5, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS)
	s.Pricing = opusPricing()
	return s
}

// opus47 is Claude Opus 4.7.
func opus47() model.Spec {
	s := base("claude-opus-4-7", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels5, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS)
	s.Pricing = opusPricing()
	return s
}

// opus46 is Claude Opus 4.6, the last generation before the xhigh effort
// level existed — hence effortLevels4 rather than effortLevels5.
func opus46() model.Spec {
	s := base("claude-opus-4-6", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels4, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS)
	s.Pricing = opusPricing()
	return s
}

// sonnet5 is Claude Sonnet 5 — the only model in this roster with more
// than one pricing tier, because Anthropic's introductory $2/$10 rate
// runs through 2026-08-31 and the standard $3/$15 rate takes over on
// 2026-09-01.
//
// The two tiers are half-open and adjacent on the time axis, so exactly
// one matches any given instant — the invariant
// docs/specifications/model/data-types.md#pricing requires and
// model.NewCapabilities checks for overlap.
func sonnet5() model.Spec {
	s := base("claude-sonnet-5", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels5, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS)

	introEnd := sonnet5IntroEnd
	standardStart := sonnet5IntroEnd

	introCacheWrite, introCacheRead := 2.50, 0.20
	introBatchIn, introBatchOut := 1.00, 5.00
	stdCacheWrite, stdCacheRead := 3.75, 0.30
	stdBatchIn, stdBatchOut := 1.50, 7.50

	s.Pricing = model.Pricing{
		Currency: currency,
		Tiers: []model.PricingTier{
			{
				// Nil EffectiveFrom means "since this plugin version was
				// published", which is the correct reading: the intro rate
				// was already in force before this build existed.
				EffectiveUntil:     &introEnd,
				InputPerMtok:       2.00,
				OutputPerMtok:      10.00,
				CacheWritePerMtok:  &introCacheWrite,
				CacheReadPerMtok:   &introCacheRead,
				BatchInputPerMtok:  &introBatchIn,
				BatchOutputPerMtok: &introBatchOut,
			},
			{
				EffectiveFrom:      &standardStart,
				InputPerMtok:       3.00,
				OutputPerMtok:      15.00,
				CacheWritePerMtok:  &stdCacheWrite,
				CacheReadPerMtok:   &stdCacheRead,
				BatchInputPerMtok:  &stdBatchIn,
				BatchOutputPerMtok: &stdBatchOut,
			},
		},
	}
	return s
}

// sonnet46 is Claude Sonnet 4.6.
func sonnet46() model.Spec {
	s := base("claude-sonnet-4-6", contextWindow1M, maxOutput128K)
	s.Thinking = effortThinking(effortLevels4, modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS)
	s.Pricing = flatPricing(3.00, 15.00, 3.75, 0.30, 1.50, 7.50)
	return s
}

// haiku45 is Claude Haiku 4.5 — the only model in this roster on the
// older token-budget reasoning control rather than the effort ladder, and
// the only one with a 200k context window and a 64k output ceiling.
//
// The budget range's upper bound is one token below MaxOutputTokens
// because Anthropic requires budget_tokens < max_tokens; the lower bound
// is Anthropic's documented 1024 minimum.
//
// It declares no EffortControl — output_config.effort errors on this
// model — and AdaptiveByDefault is false, because omitting the thinking
// parameter here means no thinking at all rather than adaptive reasoning.
// That pair is the exact opposite of every other model in this roster, and
// it is the case the older single-mode ThinkingSpec handled worst: a
// nil BudgetControl.Default now says "zero reasoning tokens by default"
// directly, where before it had to be smuggled through a Default field
// typed as a string holding "0".
func haiku45() model.Spec {
	s := base("claude-haiku-4-5", contextWindow200K, maxOutput64K)
	s.Thinking = model.ThinkingSpec{
		Supported: true,
		Budget: &model.BudgetControl{
			Range: model.ThinkingBudgetRange{
				Min: 1024,
				Max: maxOutput64K - 1,
			},
		},
		AdaptiveByDefault: false,
		Disable:           modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
	}
	s.Pricing = flatPricing(1.00, 5.00, 1.25, 0.10, 0.50, 2.50)
	return s
}
