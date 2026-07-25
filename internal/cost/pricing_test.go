package cost

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

var (
	t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 = time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	t3 = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
)

func flatTier() *modelv1.PricingTier {
	return &modelv1.PricingTier{InputPerMtok: 1, OutputPerMtok: 2}
}

// --- ResolveTier ---

func TestResolveTierSingleTierAlwaysMatches(t *testing.T) {
	t.Parallel()

	p := &modelv1.Pricing{Currency: "USD", Tiers: []*modelv1.PricingTier{flatTier()}}

	points := []struct {
		at     time.Time
		tokens int64
	}{
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 0},
		{time.Now(), 500_000},
		{time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), 1 << 40},
	}
	for _, pt := range points {
		got, err := ResolveTier(p, pt.at, pt.tokens)
		if err != nil {
			t.Fatalf("ResolveTier(%v, %d) unexpected error: %v", pt.at, pt.tokens, err)
		}
		if got != p.Tiers[0] {
			t.Fatalf("ResolveTier(%v, %d) = %v, want the single tier", pt.at, pt.tokens, got)
		}
	}
}

func TestResolveTierTimeBoundedBoundary(t *testing.T) {
	t.Parallel()

	before := &modelv1.PricingTier{EffectiveUntil: ts(t1), InputPerMtok: 1}
	atOrAfter := &modelv1.PricingTier{EffectiveFrom: ts(t1), InputPerMtok: 2}
	p := &modelv1.Pricing{Currency: "USD", Tiers: []*modelv1.PricingTier{before, atOrAfter}}

	got, err := ResolveTier(p, t1.Add(-time.Nanosecond), 0)
	if err != nil {
		t.Fatalf("just before boundary: unexpected error: %v", err)
	}
	if got != before {
		t.Fatalf("just before boundary: got tier with InputPerMtok=%v, want the 'before' tier", got.InputPerMtok)
	}

	got, err = ResolveTier(p, t1, 0)
	if err != nil {
		t.Fatalf("at boundary: unexpected error: %v", err)
	}
	if got != atOrAfter {
		t.Fatalf("at boundary: got tier with InputPerMtok=%v, want the 'atOrAfter' tier — boundary must belong to exactly one tier", got.InputPerMtok)
	}

	// Never both: re-derive matchingTierIndices directly at the boundary.
	if matched := matchingTierIndices(p.Tiers, t1, 0); len(matched) != 1 {
		t.Fatalf("boundary instant matched %d tiers, want exactly 1", len(matched))
	}
}

func TestResolveTierInputTokenBoundedBoundary(t *testing.T) {
	t.Parallel()

	const threshold = int64(200_000)
	below := &modelv1.PricingTier{InputTokensUntil: ptrI64(threshold), InputPerMtok: 1}
	atOrAbove := &modelv1.PricingTier{InputTokensFrom: ptrI64(threshold), InputPerMtok: 2}
	p := &modelv1.Pricing{Currency: "USD", Tiers: []*modelv1.PricingTier{below, atOrAbove}}

	got, err := ResolveTier(p, time.Now(), threshold-1)
	if err != nil {
		t.Fatalf("just below threshold: unexpected error: %v", err)
	}
	if got != below {
		t.Fatalf("just below threshold: got wrong tier")
	}

	got, err = ResolveTier(p, time.Now(), threshold)
	if err != nil {
		t.Fatalf("at threshold: unexpected error: %v", err)
	}
	if got != atOrAbove {
		t.Fatalf("at threshold: got wrong tier — boundary must belong to exactly one tier")
	}
}

func TestResolveTierNoMatch(t *testing.T) {
	t.Parallel()

	// Coverage only declared over [t0, t1) — anything at or after t1 is
	// genuinely outside every declared tier.
	p := &modelv1.Pricing{
		Currency: "USD",
		Tiers:    []*modelv1.PricingTier{{EffectiveFrom: ts(t0), EffectiveUntil: ts(t1), InputPerMtok: 1}},
	}

	_, err := ResolveTier(p, t1, 0)
	if !errors.Is(err, ErrNoMatchingTier) {
		t.Fatalf("ResolveTier at t1 = %v, want ErrNoMatchingTier", err)
	}

	_, err = ResolveTier(p, t0.Add(-time.Second), 0)
	if !errors.Is(err, ErrNoMatchingTier) {
		t.Fatalf("ResolveTier before t0 = %v, want ErrNoMatchingTier", err)
	}
}

func TestResolveTierNilPricing(t *testing.T) {
	t.Parallel()

	_, err := ResolveTier(nil, time.Now(), 0)
	if !errors.Is(err, ErrNoMatchingTier) {
		t.Fatalf("ResolveTier(nil, ...) = %v, want ErrNoMatchingTier", err)
	}
}

// --- ValidatePricing ---

func TestValidatePricingSingleTierPasses(t *testing.T) {
	t.Parallel()

	p := &modelv1.Pricing{Currency: "USD", Tiers: []*modelv1.PricingTier{flatTier()}}
	if err := ValidatePricing(p); err != nil {
		t.Fatalf("ValidatePricing() = %v, want nil", err)
	}
}

func TestValidatePricingCleanBoundaryPasses(t *testing.T) {
	t.Parallel()

	p := &modelv1.Pricing{
		Currency: "USD",
		Tiers: []*modelv1.PricingTier{
			{EffectiveUntil: ts(t1), InputPerMtok: 1},
			{EffectiveFrom: ts(t1), InputPerMtok: 2},
		},
	}
	if err := ValidatePricing(p); err != nil {
		t.Fatalf("ValidatePricing() = %v, want nil", err)
	}
}

func TestValidatePricingOverlapRejected(t *testing.T) {
	t.Parallel()

	// Both tiers are otherwise unbounded (nil on the outer ends) so the
	// only invariant violation anywhere on the plane is the deliberate
	// [t1, t2) overlap — isolating the overlap check from the separate
	// gap check below.
	p := &modelv1.Pricing{
		Currency: "USD",
		Tiers: []*modelv1.PricingTier{
			{EffectiveUntil: ts(t2), InputPerMtok: 1},
			{EffectiveFrom: ts(t1), InputPerMtok: 2}, // t1 < t2: overlaps [t1, t2)
		},
	}
	err := ValidatePricing(p)
	if !errors.Is(err, ErrOverlappingTiers) {
		t.Fatalf("ValidatePricing() = %v, want ErrOverlappingTiers", err)
	}
}

func TestValidatePricingGapRejected(t *testing.T) {
	t.Parallel()

	// Tier A covers [t0, t1); tier B covers [t2, t3); t1 < t2 leaves a
	// genuine gap over [t1, t2) that matches neither tier.
	p := &modelv1.Pricing{
		Currency: "USD",
		Tiers: []*modelv1.PricingTier{
			{EffectiveFrom: ts(t0), EffectiveUntil: ts(t1), InputPerMtok: 1},
			{EffectiveFrom: ts(t2), EffectiveUntil: ts(t3), InputPerMtok: 2},
		},
	}
	err := ValidatePricing(p)
	if !errors.Is(err, ErrPricingGap) {
		t.Fatalf("ValidatePricing() = %v, want ErrPricingGap", err)
	}
}

func TestValidatePricingFreeWithNoTiersPasses(t *testing.T) {
	t.Parallel()

	p := &modelv1.Pricing{Currency: "USD", Free: true}
	if err := ValidatePricing(p); err != nil {
		t.Fatalf("ValidatePricing() = %v, want nil", err)
	}
}

func TestValidatePricingNonFreeWithNoTiersRejected(t *testing.T) {
	t.Parallel()

	p := &modelv1.Pricing{Currency: "USD"}
	if err := ValidatePricing(p); !errors.Is(err, ErrPricingGap) {
		t.Fatalf("ValidatePricing() = %v, want ErrPricingGap (no tiers, not free)", err)
	}
}

func TestValidatePricingNil(t *testing.T) {
	t.Parallel()

	if err := ValidatePricing(nil); err == nil {
		t.Fatal("ValidatePricing(nil) = nil, want an error")
	}
}

// --- Tier-coverage probe table ---

// buildGridFixture returns a valid 2x2 grid of tiers partitioning the
// full (time, input_tokens) plane: two time segments (before/at-or-after
// tMid) times two input-token segments (below/at-or-above tokenThreshold).
func buildGridFixture(tMid time.Time, tokenThreshold int64) []*modelv1.PricingTier {
	return []*modelv1.PricingTier{
		{EffectiveUntil: ts(tMid), InputTokensUntil: ptrI64(tokenThreshold), InputPerMtok: 1}, // early, low
		{EffectiveUntil: ts(tMid), InputTokensFrom: ptrI64(tokenThreshold), InputPerMtok: 2},  // early, high
		{EffectiveFrom: ts(tMid), InputTokensUntil: ptrI64(tokenThreshold), InputPerMtok: 3},  // late, low
		{EffectiveFrom: ts(tMid), InputTokensFrom: ptrI64(tokenThreshold), InputPerMtok: 4},   // late, high
	}
}

func TestTierCoverageProbeTable(t *testing.T) {
	t.Parallel()

	tMid := t1
	const threshold = int64(200_000)
	tiers := buildGridFixture(tMid, threshold)
	p := &modelv1.Pricing{Currency: "USD", Tiers: tiers}

	if err := ValidatePricing(p); err != nil {
		t.Fatalf("valid grid fixture rejected: %v", err)
	}

	timeProbes := []time.Time{tMid.Add(-time.Hour), tMid, tMid.Add(time.Hour)}
	tokenProbes := []int64{threshold - 1, threshold, threshold + 1}

	for _, at := range timeProbes {
		for _, tok := range tokenProbes {
			matched := matchingTierIndices(tiers, at, tok)
			if len(matched) != 1 {
				t.Errorf("probe (at=%v, tokens=%d): matched %d tiers (indices %v), want exactly 1", at, tok, len(matched), matched)
			}
		}
	}

	t.Run("mutated to introduce a gap is caught", func(t *testing.T) {
		t.Parallel()
		gapped := buildGridFixture(tMid, threshold)
		gapped = append(gapped[:1], gapped[2:]...) // drop the "early, high" tier
		if err := ValidatePricing(&modelv1.Pricing{Currency: "USD", Tiers: gapped}); !errors.Is(err, ErrPricingGap) {
			t.Fatalf("mutated (gap) fixture: ValidatePricing() = %v, want ErrPricingGap", err)
		}
	})

	t.Run("mutated to introduce an overlap is caught", func(t *testing.T) {
		t.Parallel()
		overlapped := buildGridFixture(tMid, threshold)
		// Widen the "early, low" tier's token range so it also covers the
		// "early, high" tier's territory.
		overlapped[0].InputTokensUntil = nil
		if err := ValidatePricing(&modelv1.Pricing{Currency: "USD", Tiers: overlapped}); !errors.Is(err, ErrOverlappingTiers) {
			t.Fatalf("mutated (overlap) fixture: ValidatePricing() = %v, want ErrOverlappingTiers", err)
		}
	})
}

// --- internal helpers ---

func TestTierMatchesNilTierNeverMatches(t *testing.T) {
	t.Parallel()

	if tierMatches(nil, time.Now(), 0) {
		t.Fatal("tierMatches(nil, ...) = true, want false")
	}
}

func TestPrevInt64Saturates(t *testing.T) {
	t.Parallel()

	const minInt64 = -1 << 63
	if got := prevInt64(minInt64); got != minInt64 {
		t.Fatalf("prevInt64(MinInt64) = %d, want %d (saturate, not overflow)", got, minInt64)
	}
	if got := prevInt64(5); got != 4 {
		t.Fatalf("prevInt64(5) = %d, want 4", got)
	}
}

func TestTokenBreakpointsDedupesAndIgnoresFullyUnboundedTiers(t *testing.T) {
	t.Parallel()

	tiers := []*modelv1.PricingTier{
		{InputPerMtok: 1}, // fully unbounded: contributes no breakpoints
		{InputTokensFrom: ptrI64(100), InputTokensUntil: ptrI64(200)},
		{InputTokensFrom: ptrI64(100)}, // duplicate 100 must not appear twice
	}
	got := tokenBreakpoints(tiers)
	want := []int64{100, 200}
	if len(got) != len(want) {
		t.Fatalf("tokenBreakpoints() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokenBreakpoints() = %v, want %v", got, want)
		}
	}
}

// TestGapDetectionCatchesWhatPkgModelMisses is the task's explicit
// requirement: pkg/model/capabilities.go's validatePricing is
// overlap-only, by its own documented judgment call, and does not detect
// a gap between two non-overlapping tiers. This test builds exactly that
// fixture — tier A covers [t0, t1), tier B covers [t2, t3), t1 < t2 — and
// confirms pkg/model.NewCapabilities accepts it (the documented gap)
// while this package's ValidatePricing rejects it.
func TestGapDetectionCatchesWhatPkgModelMisses(t *testing.T) {
	t.Parallel()

	kernelSide := &modelv1.Pricing{
		Currency: "USD",
		Tiers: []*modelv1.PricingTier{
			{EffectiveFrom: ts(t0), EffectiveUntil: ts(t1), InputPerMtok: 1, OutputPerMtok: 2},
			{EffectiveFrom: ts(t2), EffectiveUntil: ts(t3), InputPerMtok: 3, OutputPerMtok: 4},
		},
	}
	if err := ValidatePricing(kernelSide); !errors.Is(err, ErrPricingGap) {
		t.Fatalf("internal/cost.ValidatePricing() = %v, want ErrPricingGap", err)
	}

	sdkSide := model.Pricing{
		Currency: "USD",
		Tiers: []model.PricingTier{
			{EffectiveFrom: &t0, EffectiveUntil: &t1, InputPerMtok: 1, OutputPerMtok: 2},
			{EffectiveFrom: &t2, EffectiveUntil: &t3, InputPerMtok: 3, OutputPerMtok: 4},
		},
	}
	spec := model.Spec{
		ID:       "gap-fixture-model",
		Thinking: model.ThinkingSpec{Mode: modelv1.ThinkingMode_THINKING_MODE_NONE},
		Pricing:  sdkSide,
	}
	if _, err := model.NewCapabilities([]model.Spec{spec}, &configv1.ConfigSchema{}); err != nil {
		t.Fatalf("pkg/model.NewCapabilities() = %v, want nil (its validatePricing is documented as overlap-only and should accept this gapped fixture) — if this now fails, pkg/model gained gap detection and this test's premise (and its comment) needs updating", err)
	}
}

func TestIsFree(t *testing.T) {
	t.Parallel()

	tier := &modelv1.PricingTier{InputPerMtok: 1}
	tests := []struct {
		name string
		p    *modelv1.Pricing
		want bool
	}{
		{"nil pricing", nil, false},
		{"free with no tiers", &modelv1.Pricing{Currency: "USD", Free: true}, true},
		{"paid with no tiers", &modelv1.Pricing{Currency: "USD"}, false},
		{"paid with tiers", &modelv1.Pricing{Currency: "USD", Tiers: []*modelv1.PricingTier{tier}}, false},
		// A free Pricing that also declares tiers is deliberately not
		// "free" here: ValidatePricing validates those tiers like any
		// other, so a caller must resolve against them.
		{"free with tiers", &modelv1.Pricing{Currency: "USD", Free: true, Tiers: []*modelv1.PricingTier{tier}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsFree(tc.p); got != tc.want {
				t.Errorf("IsFree = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsFree_agreesWithValidatePricing pins the invariant the helper
// exists for: exactly the tier-less shape ValidatePricing accepts is the
// shape ResolveTier cannot serve.
func TestIsFree_agreesWithValidatePricing(t *testing.T) {
	t.Parallel()

	p := &modelv1.Pricing{Currency: "USD", Free: true}
	if err := ValidatePricing(p); err != nil {
		t.Fatalf("ValidatePricing accepts a tier-less free Pricing: %v", err)
	}
	if _, err := ResolveTier(p, time.Now(), 1); !errors.Is(err, ErrNoMatchingTier) {
		t.Fatalf("ResolveTier on a tier-less free Pricing = %v, want ErrNoMatchingTier", err)
	}
	if !IsFree(p) {
		t.Error("IsFree = false for the one shape both of the above describe")
	}
}
