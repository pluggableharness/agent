package modeltest

import (
	"context"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// DefaultCallTimeout bounds every RPC the suite issues, so a provider
// that hangs produces a clear finding rather than stalling until the
// caller's own deadline. Override it with WithCallTimeout.
const DefaultCallTimeout = 30 * time.Second

// runSuite drives every check against client and returns what it found.
// Both the in-process and subprocess entry points funnel here, so the two
// modes can never drift apart in what they assert.
func runSuite(ctx context.Context, client modelv1.ModelServiceClient, cfg *config) Report {
	var findings []Finding
	rec := newRecorder(&findings)

	checkDescribe(ctx, rec.sub("Describe"), client, cfg)

	caps := checkCapabilities(ctx, rec.sub("Capabilities"), client, cfg)
	if caps == nil {
		// Every remaining check needs a model to address. Continuing would
		// report a cascade of findings that all have one cause.
		return Report{Findings: findings}
	}

	if !checkConfigure(ctx, rec.sub("Configure"), client, cfg) {
		return Report{Findings: findings}
	}

	if cfg.skipStream {
		rec.sub("StreamCompletion").skipf("behavioral",
			"disabled by WithoutStreamCompletion; every behavioral requirement is unchecked")
		return Report{Findings: findings}
	}

	modelID := cfg.modelID
	if modelID == "" {
		modelID = caps.GetModels()[0].GetId()
	}
	spec := specByID(caps.GetModels(), modelID)
	if spec == nil {
		rec.failf("model-selection", "the selected model %q is not advertised by this provider", modelID)
		return Report{Findings: findings}
	}

	checkStream(ctx, rec.sub("StreamCompletion"), client, cfg, spec)
	checkCancellation(ctx, rec.sub("Cancellation"), client, cfg, modelID)
	checkUnknownModel(ctx, rec.sub("UnknownModel"), client, cfg)
	checkCapabilityGates(ctx, rec.sub("CapabilityGates"), client, cfg, spec)

	return Report{Findings: findings}
}

// checkDescribe asserts the plugin reports a well-formed identity.
func checkDescribe(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config) {
	ctx, cancel := context.WithTimeout(ctx, cfg.callTimeout)
	defer cancel()

	resp, err := client.Describe(ctx, &modelv1.DescribeRequest{})
	if err != nil {
		rec.failf("implemented", "Describe is a MUST and returned %v", err)
		return
	}
	p := resp.GetProducer()
	if p.GetName() == "" {
		rec.failf("name", "Describe reported an empty name; a dev_overrides binary has no lock-file entry to fall back on")
	}
	if p.GetCategory() != commonv1.Category_CATEGORY_MODEL {
		rec.failf("category", "Describe reported category %v, want CATEGORY_MODEL", p.GetCategory())
	}
	if cfg.inProcess && cfg.identity != (identityExpectation{}) {
		// The identity a plugin reports is a property of its own binary.
		// In-process, modeltest supplies it, so there is nothing of the
		// provider's to verify — say so rather than passing a check that
		// only ever compares modeltest against itself.
		rec.skipf("expected-identity",
			"identity expectations apply to a built binary; in-process the identity is modeltest's own. Use RunBinary or CheckBinary")
		return
	}
	if want := cfg.identity.name; want != "" && p.GetName() != want {
		rec.failf("expected-name", "Describe reported name %q, want %q", p.GetName(), want)
	}
	if want := cfg.identity.version; want != "" && p.GetVersion() != want {
		rec.failf("expected-version", "Describe reported version %q, want %q", p.GetVersion(), want)
	}
	if want := cfg.identity.source; want != "" && p.GetSource() != want {
		rec.failf("expected-source", "Describe reported source %q, want %q", p.GetSource(), want)
	}
}

// checkCapabilities asserts the advertisement's own invariants and
// returns it, or nil when it is unusable.
func checkCapabilities(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config) *modelv1.Capabilities {
	ctx, cancel := context.WithTimeout(ctx, cfg.callTimeout)
	defer cancel()

	resp, err := client.GetCapabilities(ctx, &modelv1.GetCapabilitiesRequest{})
	if err != nil {
		rec.failf("implemented", "GetCapabilities is a MUST and returned %v", err)
		return nil
	}
	caps := resp.GetCapabilities()
	if len(caps.GetModels()) == 0 {
		rec.failf("models", "no models are advertised, which makes this provider unroutable")
		return nil
	}
	if caps.GetConfigSchema() == nil {
		rec.failf("config-schema", "no ConfigSchema is advertised; the kernel needs it before it can call Configure")
	}

	seen := make(map[string]bool, len(caps.GetModels()))
	for i, spec := range caps.GetModels() {
		id := spec.GetId()
		if id == "" {
			rec.failf("model-id", "the model at index %d has an empty id", i)
			continue
		}
		if seen[id] {
			rec.failf("model-id", "model id %q is advertised more than once, so routing to it is ambiguous", id)
		}
		seen[id] = true
		checkModelSpec(rec.sub(id), spec)
	}
	return caps
}

// checkModelSpec asserts one advertised model's declarative invariants.
func checkModelSpec(rec *recorder, spec *modelv1.ModelSpec) {
	if spec.GetContextWindow() <= 0 {
		rec.failf("context-window", "context_window is %d, want a positive budget", spec.GetContextWindow())
	}
	if spec.GetMaxOutputTokens() <= 0 {
		rec.failf("max-output-tokens", "max_output_tokens is %d, want a positive ceiling", spec.GetMaxOutputTokens())
	}
	checkThinkingSpec(rec.sub("thinking"), spec.GetThinking())
	checkCachingSpec(rec.sub("caching"), spec.GetCaching())
	checkPricing(rec.sub("pricing"), spec.GetPricing(), spec.GetCaching().GetSupported())
}

// checkThinkingSpec asserts ThinkingSpec's per-axis invariants
// (docs/specifications/model/data-types.md#thinkingspec).
func checkThinkingSpec(rec *recorder, ts *modelv1.ThinkingSpec) {
	if !ts.GetSupported() {
		if ts.GetEffort() != nil || ts.GetBudget() != nil {
			rec.failf("unsupported", "thinking is unsupported but a reasoning control is declared")
		}
		if ts.GetAdaptiveByDefault() {
			rec.failf("unsupported", "thinking is unsupported but adaptive_by_default is set")
		}
		switch ts.GetDisable() {
		case modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
			modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_CONDITIONAL:
			rec.failf("unsupported", "thinking is unsupported but disable claims reasoning can be turned off")
		default:
		}
		return
	}

	if ts.GetDisable() == modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_UNSPECIFIED {
		rec.failf("disable", "thinking is supported but disable is unset, so the kernel cannot tell whether it may turn reasoning off")
	}
	if e := ts.GetEffort(); e != nil {
		switch {
		case len(e.GetLevels()) == 0:
			rec.failf("effort", "an effort control is declared with no levels; a model without one omits the control instead")
		case e.GetDefault() == "":
			rec.failf("effort", "an effort control is declared with no default level")
		case !slices.Contains(e.GetLevels(), e.GetDefault()):
			// The default exists so the kernel can send it as an explicit
			// override; naming a level the vendor rejects makes that
			// override a guaranteed error.
			rec.failf("effort", "the effort default %q is not one of the declared levels %v", e.GetDefault(), e.GetLevels())
		}
	}
	if b := ts.GetBudget(); b != nil {
		r := b.GetRange()
		switch {
		case r == nil:
			rec.failf("budget", "a budget control is declared with no range")
		case r.GetMin() > r.GetMax():
			rec.failf("budget", "the budget range [%d, %d] is inverted", r.GetMin(), r.GetMax())
		case r.GetMax() <= 0:
			// A control admitting only a budget of zero declares a
			// capability no caller can use; a model with no budget control
			// omits it instead.
			rec.failf("budget", "the budget range [%d, %d] admits no usable budget", r.GetMin(), r.GetMax())
		case b.Default != nil && (b.GetDefault() < r.GetMin() || b.GetDefault() > r.GetMax()):
			rec.failf("budget", "the budget default %d is outside the declared range [%d, %d]", b.GetDefault(), r.GetMin(), r.GetMax())
		}
	}
}

// checkCachingSpec asserts CachingSpec's per-axis invariants.
func checkCachingSpec(rec *recorder, cs *modelv1.CachingSpec) {
	if !cs.GetSupported() {
		if cs.GetExplicitMarkers() || cs.GetImplicitAutomatic() {
			rec.failf("unsupported", "caching is unsupported but a caching mechanism is declared")
		}
		return
	}
	if !cs.GetExplicitMarkers() && !cs.GetImplicitAutomatic() {
		rec.failf("mechanism", "caching is supported but neither mechanism is declared, which reads as no caching to every caller")
	}
}

// checkPricing asserts Pricing's coverage invariant: exactly one tier
// matches any (timestamp, input_token_count) pair.
func checkPricing(rec *recorder, p *modelv1.Pricing, cachingSupported bool) {
	if p == nil {
		rec.failf("present", "no pricing is declared; it is required on every model, including a free one")
		return
	}
	if p.GetCurrency() == "" {
		rec.failf("currency", "no pricing currency is declared")
	}
	if p.GetFree() {
		return
	}
	if len(p.GetTiers()) == 0 {
		rec.failf("tiers", "no pricing tiers are declared and the model is not marked free")
		return
	}

	for i, tier := range p.GetTiers() {
		if tier.GetInputPerMtok() < 0 || tier.GetOutputPerMtok() < 0 {
			rec.failf("rates", "tier %d declares a negative rate", i)
		}
		if cachingSupported && (tier.CacheWritePerMtok == nil || tier.CacheReadPerMtok == nil) {
			rec.failf("cache-rates", "tier %d omits a cache rate on a model that supports caching, so cached turns cannot be priced", i)
		}
	}

	// Overlap makes cost non-deterministic, and the kernel persists
	// cost_usd at usage-event time — so a wrong pick is wrong in the
	// ledger forever, with nothing to notice it by.
	tiers := p.GetTiers()
	for i := range tiers {
		for j := i + 1; j < len(tiers); j++ {
			if tiersOverlap(tiers[i], tiers[j]) {
				rec.failf("tier-overlap",
					"tiers %d and %d overlap; exactly one tier must match any (timestamp, input_token_count) pair", i, j)
			}
		}
	}
}

// tiersOverlap reports whether a and b both match some (timestamp,
// input_token_count) pair. Both dimensions are half-open, so an overlap
// requires overlapping on both.
func tiersOverlap(a, b *modelv1.PricingTier) bool {
	return timeRangesOverlap(a, b) && tokenRangesOverlap(a, b)
}

func timeRangesOverlap(a, b *modelv1.PricingTier) bool {
	// An absent bound is unbounded on that side.
	aFrom, aUntil := a.GetEffectiveFrom(), a.GetEffectiveUntil()
	bFrom, bUntil := b.GetEffectiveFrom(), b.GetEffectiveUntil()
	if aUntil != nil && bFrom != nil && !aUntil.AsTime().After(bFrom.AsTime()) {
		return false
	}
	if bUntil != nil && aFrom != nil && !bUntil.AsTime().After(aFrom.AsTime()) {
		return false
	}
	return true
}

func tokenRangesOverlap(a, b *modelv1.PricingTier) bool {
	aFrom, aUntil := a.InputTokensFrom, a.InputTokensUntil
	bFrom, bUntil := b.InputTokensFrom, b.InputTokensUntil
	if aUntil != nil && bFrom != nil && *aUntil <= *bFrom {
		return false
	}
	if bUntil != nil && aFrom != nil && *bUntil <= *aFrom {
		return false
	}
	return true
}

// checkConfigure calls Configure and reports whether it succeeded. A
// failure here stops the run, because every behavioral check afterwards
// would fail for this one reason.
func checkConfigure(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config) bool {
	ctx, cancel := context.WithTimeout(ctx, cfg.callTimeout)
	defer cancel()

	if _, err := client.Configure(ctx, &modelv1.ConfigureRequest{Config: cfg.configure}); err != nil {
		rec.failf("accepts-config",
			"Configure returned %v; pass this provider's configuration with modeltest.WithConfig", err)
		return false
	}

	// Configure MUST be safely re-callable
	// (docs/specifications/model/protocol.md#configure). The kernel calls
	// it once today, so a provider that only works the first time looks
	// fine in production right up until a credential rotation needs it —
	// which is exactly when a failure is most expensive. Calling it twice
	// here is the whole check: a provider that merges rather than replaces,
	// or that panics on a rebuilt client, fails now instead of later.
	if _, err := client.Configure(ctx, &modelv1.ConfigureRequest{Config: cfg.configure}); err != nil {
		rec.failf("re-callable",
			"a second Configure returned %v; it MUST be safely re-callable, replacing configured state wholesale", err)
		return false
	}
	return true
}

// specByID finds the advertised model with the given id.
func specByID(models []*modelv1.ModelSpec, id string) *modelv1.ModelSpec {
	for _, m := range models {
		if m.GetId() == id {
			return m
		}
	}
	return nil
}

// statusCode extracts a gRPC code from err, or codes.OK for nil.
func statusCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return grpcstatus.Code(err)
}
