package cost

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

var (
	// ErrNoMatchingTier is returned by ResolveTier when no tier in a
	// Pricing matches the given (timestamp, inputTokens) pair — a gap,
	// which ValidatePricing should have caught at capability-load time,
	// but ResolveTier must still fail safely rather than picking an
	// arbitrary tier or computing a wrong cost.
	ErrNoMatchingTier = errors.New("cost: no pricing tier matches")

	// ErrOverlappingTiers is returned by ValidatePricing when two or
	// more tiers both match some (timestamp, inputTokens) pair.
	ErrOverlappingTiers = errors.New("cost: overlapping pricing tiers")

	// ErrPricingGap is returned by ValidatePricing when some
	// (timestamp, inputTokens) pair within the tiers' overall declared
	// range is matched by no tier at all.
	ErrPricingGap = errors.New("cost: gap in pricing tier coverage")

	// errNilPricing guards ValidatePricing/ResolveTier against a nil
	// *modelv1.Pricing — a construction bug in the caller, not a
	// tier-coverage question, so it is not one of the three sentinels
	// above.
	errNilPricing = errors.New("cost: pricing is nil")
)

// ValidatePricing enforces docs/specifications/model/data-types.md#pricing's
// "exactly one tier MUST match at any given (timestamp, input_tokens)
// pair" — rejecting both overlaps and gaps at capability-load time (i.e.
// when a model provider's GetCapabilities response is first validated,
// before any completion is ever billed against it).
//
// Half-open interval semantics (both dimensions independently
// half-open, matching docs/specifications/model/data-types.md#pricing's
// "Kernel resolution" paragraph): a tier matches a given (timestamp,
// inputTokens) pair when BOTH:
//
//   - effective_from <= timestamp < effective_until, with a nil
//     effective_from meaning "unbounded below" (matches back to the
//     start of time) and a nil effective_until meaning "unbounded above,
//     still current" (matches forever forward).
//   - input_tokens_from <= inputTokens < input_tokens_until, with a nil
//     input_tokens_from meaning "unbounded below" and a nil
//     input_tokens_until meaning "unbounded above".
//
// A tier whose bounds are all nil in both dimensions matches every
// (timestamp, inputTokens) pair — the degenerate single-tier case.
//
// Detection method: because every tier is an axis-aligned rectangle in
// the (time, input_tokens) plane, the match-count function (how many
// tiers match a given point) can only change value at one of the
// declared bounds — nowhere else. So the coverage plane decomposes into
// a finite grid: one dimension's distinct declared bounds (effective_from
// and effective_until values across every tier) times the other
// dimension's distinct declared bounds (input_tokens_from and
// input_tokens_until values across every tier). Probing exactly one
// representative point per grid cell — including the two unbounded outer
// cells in each dimension — is sufficient to detect any overlap
// (count > 1 at some cell) or gap (count == 0 at some cell) that could
// exist anywhere on the plane, not just at the probed points themselves.
// This is what makes gap detection tractable rather than requiring a
// full symbolic reconstruction of the covered region.
func ValidatePricing(p *modelv1.Pricing) error {
	if p == nil {
		return errNilPricing
	}
	tiers := p.GetTiers()
	if len(tiers) == 0 {
		if p.GetFree() {
			return nil
		}
		return fmt.Errorf("%w: at least one tier required unless free", ErrPricingGap)
	}

	timeSamples := sampleTimePoints(timeBreakpoints(tiers))
	tokenSamples := sampleTokenPoints(tokenBreakpoints(tiers))

	for _, at := range timeSamples {
		for _, tok := range tokenSamples {
			matched := matchingTierIndices(tiers, at, tok)
			switch len(matched) {
			case 0:
				return fmt.Errorf("%w: no tier matches at effective time %s, input_tokens %d", ErrPricingGap, at.Format(time.RFC3339Nano), tok)
			case 1:
				// Exactly one match — this cell is fine.
			default:
				return fmt.Errorf("%w: tiers %v all match at effective time %s, input_tokens %d", ErrOverlappingTiers, matched, at.Format(time.RFC3339Nano), tok)
			}
		}
	}
	return nil
}

// IsFree reports whether p is a free model declared with no tiers at all
// — the one shape ValidatePricing accepts without any tier coverage, and
// therefore the one shape ResolveTier can never resolve.
//
// A caller computing a completion's cost MUST check this before calling
// ResolveTier: a `free = true, tiers = []` Pricing is legal per
// data-types.md#pricing and ValidatePricing lets it through, so treating
// ResolveTier's ErrNoMatchingTier as the only outcome would make every
// free model unusable — it would fail its first completion rather than
// bill it at zero.
//
// A free Pricing that also declares tiers is deliberately NOT covered
// here: ValidatePricing validates those tiers like any other, so they are
// real rates a caller must resolve against rather than assume away.
func IsFree(p *modelv1.Pricing) bool {
	return p.GetFree() && len(p.GetTiers()) == 0
}

// ResolveTier finds the single PricingTier in p matching both at (a
// timestamp) and inputTokens (the completion's input token count), per
// docs/specifications/model/protocol.md#cost-computation's per-event
// resolution rule. Returns ErrNoMatchingTier if none matches (should be
// unreachable for a Pricing that already passed ValidatePricing, but
// must never panic or silently pick a wrong tier).
//
// If more than one tier matches (only possible for a Pricing that has
// not been validated by ValidatePricing), ResolveTier deterministically
// returns the first match in p.Tiers' declared order rather than
// panicking or picking arbitrarily — ValidatePricing, not ResolveTier, is
// the enforcement point for "exactly one tier matches."
func ResolveTier(p *modelv1.Pricing, at time.Time, inputTokens int64) (*modelv1.PricingTier, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: %w", ErrNoMatchingTier, errNilPricing)
	}
	for _, t := range p.GetTiers() {
		if tierMatches(t, at, inputTokens) {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w: effective time %s, input_tokens %d", ErrNoMatchingTier, at.Format(time.RFC3339Nano), inputTokens)
}

// tierMatches reports whether t matches at (a timestamp) and
// inputTokens, per ValidatePricing's doc comment on half-open interval
// semantics. A nil t never matches (mirrors the proto getters' nil
// safety without ever picking a wrong tier).
func tierMatches(t *modelv1.PricingTier, at time.Time, inputTokens int64) bool {
	if t == nil {
		return false
	}
	if from := t.GetEffectiveFrom(); from != nil && at.Before(from.AsTime()) {
		return false
	}
	if until := t.GetEffectiveUntil(); until != nil && !at.Before(until.AsTime()) {
		return false
	}
	if from := t.InputTokensFrom; from != nil && inputTokens < *from {
		return false
	}
	if until := t.InputTokensUntil; until != nil && inputTokens >= *until {
		return false
	}
	return true
}

// matchingTierIndices returns the indices into tiers of every tier
// matching (at, inputTokens), used by ValidatePricing's grid probe to
// report which tiers collided on an overlap.
func matchingTierIndices(tiers []*modelv1.PricingTier, at time.Time, inputTokens int64) []int {
	var matched []int
	for i, t := range tiers {
		if tierMatches(t, at, inputTokens) {
			matched = append(matched, i)
		}
	}
	return matched
}

// timeBreakpoints collects every distinct effective_from/effective_until
// value declared across tiers, sorted ascending — the grid lines in the
// time dimension per ValidatePricing's detection-method doc comment.
func timeBreakpoints(tiers []*modelv1.PricingTier) []time.Time {
	seen := make(map[int64]time.Time)
	add := func(ts *timestamppb.Timestamp) {
		if ts == nil {
			return
		}
		t := ts.AsTime()
		seen[t.UnixNano()] = t
	}
	for _, t := range tiers {
		add(t.GetEffectiveFrom())
		add(t.GetEffectiveUntil())
	}
	out := make([]time.Time, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// tokenBreakpoints collects every distinct input_tokens_from/
// input_tokens_until value declared across tiers, sorted ascending — the
// grid lines in the input-token dimension per ValidatePricing's
// detection-method doc comment.
func tokenBreakpoints(tiers []*modelv1.PricingTier) []int64 {
	seen := make(map[int64]struct{})
	add := func(v *int64) {
		if v == nil {
			return
		}
		seen[*v] = struct{}{}
	}
	for _, t := range tiers {
		add(t.InputTokensFrom)
		add(t.InputTokensUntil)
	}
	out := make([]int64, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// sampleTimePoints turns sorted breakpoints into one representative
// sample per grid cell: a point strictly before the first breakpoint
// (the unbounded-below cell), then each breakpoint itself (representing
// the half-open cell starting there, up to the next breakpoint or
// unbounded above for the last one). With zero breakpoints (neither
// dimension bound ever declared), the whole axis is a single unbounded
// cell, sampled at an arbitrary reference point.
func sampleTimePoints(breakpoints []time.Time) []time.Time {
	if len(breakpoints) == 0 {
		return []time.Time{time.Unix(0, 0).UTC()}
	}
	samples := make([]time.Time, 0, len(breakpoints)+1)
	samples = append(samples, breakpoints[0].Add(-time.Nanosecond))
	samples = append(samples, breakpoints...)
	return samples
}

// sampleTokenPoints is sampleTimePoints' int64-dimension counterpart.
func sampleTokenPoints(breakpoints []int64) []int64 {
	if len(breakpoints) == 0 {
		return []int64{0}
	}
	samples := make([]int64, 0, len(breakpoints)+1)
	samples = append(samples, prevInt64(breakpoints[0]))
	samples = append(samples, breakpoints...)
	return samples
}

// prevInt64 returns v-1, saturating at math.MinInt64 instead of
// overflowing — a real Pricing value never declares a bound anywhere
// near that extreme, but this keeps the sampler total rather than
// panicking or wrapping around on a pathological input.
func prevInt64(v int64) int64 {
	const minInt64 = -1 << 63
	if v == minInt64 {
		return v
	}
	return v - 1
}
