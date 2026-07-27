package catalog

import (
	"math"
	"testing"
	"time"

	"github.com/pluggableharness/agent/pkg/config"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// TestModels_satisfiesCapabilityValidation is the load-bearing test in
// this package: model.NewCapabilities enforces every MUST-level invariant
// docs/specifications/model/data-types.md states about ModelSpec,
// ThinkingSpec, and Pricing, so a roster that survives it is a roster the
// kernel will accept.
func TestModels_satisfiesCapabilityValidation(t *testing.T) {
	t.Parallel()

	schema, err := config.Schema()
	if err != nil {
		t.Fatalf("config.Schema: %v", err)
	}
	if _, err := model.NewCapabilities(Models(), schema); err != nil {
		t.Fatalf("NewCapabilities(Models()): %v", err)
	}
}

// TestModels_idsAreUniqueAndNonEmpty guards the one roster-level property
// NewCapabilities does not check: two entries claiming the same id would
// make model selection ambiguous.
func TestModels_idsAreUniqueAndNonEmpty(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(Models()))
	for _, m := range Models() {
		if m.ID == "" {
			t.Fatal("a model has an empty id")
		}
		if seen[m.ID] {
			t.Errorf("duplicate model id %q", m.ID)
		}
		seen[m.ID] = true
	}
	if len(seen) == 0 {
		t.Fatal("roster is empty")
	}
}

// TestModels_returnsAFreshCopy proves a caller mutating what Models
// returned cannot corrupt what the next caller sees. GetCapabilities is
// called repeatedly over a process's life, so a shared package-level
// slice would be a real aliasing hazard rather than a theoretical one.
func TestModels_returnsAFreshCopy(t *testing.T) {
	t.Parallel()

	first := Models()
	first[0].ID = "mutated"
	first[0].Pricing.Tiers[0].InputPerMtok = 999
	first[0].Thinking.Effort.Levels[0] = "mutated"

	second := Models()
	if second[0].ID == "mutated" {
		t.Error("mutating a returned Spec.ID changed the next call's roster")
	}
	if second[0].Pricing.Tiers[0].InputPerMtok == 999 {
		t.Error("mutating a returned PricingTier changed the next call's roster")
	}
	if second[0].Thinking.Effort.Levels[0] == "mutated" {
		t.Error("mutating a returned effort Levels slice changed the next call's roster")
	}
}

// TestPricing_multipliersMatchAnthropicsPublishedRatios is a transcription
// guard, not a restatement of the data. Anthropic publishes the cache and
// batch rates as fixed multipliers of the base input/output rate —
// 5-minute cache write 1.25x input, cache read 0.1x input, batch 0.5x both
// directions — so a mistyped digit in any of those four figures shows up
// here as a broken ratio even though the number still looks plausible on
// its own.
func TestPricing_multipliersMatchAnthropicsPublishedRatios(t *testing.T) {
	t.Parallel()

	for _, m := range Models() {
		for i, tier := range m.Pricing.Tiers {
			checkRatio(t, m.ID, i, "cache write", *tier.CacheWritePerMtok, tier.InputPerMtok*1.25)
			checkRatio(t, m.ID, i, "cache read", *tier.CacheReadPerMtok, tier.InputPerMtok*0.10)
			checkRatio(t, m.ID, i, "batch input", *tier.BatchInputPerMtok, tier.InputPerMtok*0.50)
			checkRatio(t, m.ID, i, "batch output", *tier.BatchOutputPerMtok, tier.OutputPerMtok*0.50)
		}
	}
}

// checkRatio compares two dollar-per-MTok figures with a tolerance well
// below a cent per million tokens — tight enough that a transcription
// error cannot hide, loose enough that binary floating point cannot
// produce a spurious failure.
func checkRatio(t *testing.T, id string, tier int, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s tier %d: %s = %v, want %v (Anthropic's published multiplier)", id, tier, label, got, want)
	}
}

// TestPricing_outputCostsMoreThanInput is a coarse sanity check that
// catches a swapped pair — the transcription error the multiplier test
// above cannot see, because swapping input and output preserves neither
// ratio but would survive a careless reading of a single row.
func TestPricing_outputCostsMoreThanInput(t *testing.T) {
	t.Parallel()

	for _, m := range Models() {
		for i, tier := range m.Pricing.Tiers {
			if tier.OutputPerMtok <= tier.InputPerMtok {
				t.Errorf("%s tier %d: output %v is not dearer than input %v — a swapped pair?",
					m.ID, i, tier.OutputPerMtok, tier.InputPerMtok)
			}
		}
	}
}

// TestSonnet5_exactlyOneTierMatchesAnyInstant exercises the roster's only
// multi-tier model against the resolution rule
// docs/specifications/model/data-types.md#pricing states: exactly one tier
// MUST match any given (timestamp, input_token_count) pair. The kernel
// resolves the tier per usage event, so a gap or an overlap here would be
// a wrong ledger row rather than a startup failure.
func TestSonnet5_exactlyOneTierMatchesAnyInstant(t *testing.T) {
	t.Parallel()

	spec := findModel(t, "claude-sonnet-5")
	if len(spec.Pricing.Tiers) != 2 {
		t.Fatalf("claude-sonnet-5 has %d tiers, want 2 (intro + standard)", len(spec.Pricing.Tiers))
	}

	tests := []struct {
		name      string
		at        time.Time
		wantInput float64
	}{
		{"well inside the intro window", time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC), 2.00},
		{"the last instant of the intro window", sonnet5IntroEnd.Add(-time.Nanosecond), 2.00},
		{"the first instant of standard pricing", sonnet5IntroEnd, 3.00},
		{"well after the cutover", time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC), 3.00},
		{"long before this build existed", time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC), 2.00},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var matched []model.PricingTier
			for _, tier := range spec.Pricing.Tiers {
				if tierCovers(tier, tc.at) {
					matched = append(matched, tier)
				}
			}
			if len(matched) != 1 {
				t.Fatalf("%d tiers match %s, want exactly 1", len(matched), tc.at)
			}
			if matched[0].InputPerMtok != tc.wantInput {
				t.Errorf("input rate at %s = %v, want %v", tc.at, matched[0].InputPerMtok, tc.wantInput)
			}
		})
	}
}

// tierCovers reports whether at falls in tier's half-open
// [EffectiveFrom, EffectiveUntil) window, treating a nil bound as
// unbounded on that side.
func tierCovers(tier model.PricingTier, at time.Time) bool {
	if tier.EffectiveFrom != nil && at.Before(*tier.EffectiveFrom) {
		return false
	}
	if tier.EffectiveUntil != nil && !at.Before(*tier.EffectiveUntil) {
		return false
	}
	return true
}

// TestThinking_modeMatchesTheDeclaredControls checks each model's
// ThinkingSpec is internally coherent beyond what validateThinkingSpec
// already enforces — specifically that an effort-controlled model quotes a
// ladder containing its own declared default, and that a budget-controlled
// model's range is ordered and fits inside its output ceiling.
func TestThinking_declaredControlsAreInternallyConsistent(t *testing.T) {
	t.Parallel()

	for _, m := range Models() {
		if !m.Thinking.Supported {
			t.Errorf("%s: every model in this roster reasons; Supported is false", m.ID)
			continue
		}
		if m.Thinking.Effort == nil && m.Thinking.Budget == nil {
			t.Errorf("%s: reasoning declared with neither an effort nor a budget control", m.ID)
			continue
		}
		if e := m.Thinking.Effort; e != nil {
			if !contains(e.Levels, e.Default) {
				t.Errorf("%s: default effort %q is absent from %v", m.ID, e.Default, e.Levels)
			}
		}
		if b := m.Thinking.Budget; b != nil {
			r := b.Range
			if r.Min <= 0 || r.Min >= r.Max {
				t.Errorf("%s: budget range [%d,%d] is not an ordered positive range", m.ID, r.Min, r.Max)
			}
			if r.Max >= m.MaxOutputTokens {
				t.Errorf("%s: budget max %d is not below max output %d, which the vendor rejects",
					m.ID, r.Max, m.MaxOutputTokens)
			}
		}
		if m.Thinking.Disable == modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_UNSPECIFIED {
			t.Errorf("%s: reasoning declared without a disable value", m.ID)
		}
	}
}

// TestThinking_effortModelsAreAdaptiveByDefault pins the pairing the older
// single-mode ThinkingSpec could not express and that
// internal/anthropic/messages relies on: Anthropic's effort ladder rides
// on top of adaptive reasoning rather than replacing it, so buildThinking
// emits thinking:{type:"adaptive"} AND output_config.effort together.
func TestThinking_effortModelsAreAdaptiveByDefault(t *testing.T) {
	t.Parallel()

	for _, m := range Models() {
		if m.Thinking.Effort == nil {
			continue
		}
		if !m.Thinking.AdaptiveByDefault {
			t.Errorf("%s: declares an effort ladder but not AdaptiveByDefault", m.ID)
		}
	}
}

// TestCaching_everyModelDeclaresExplicitMarkers pins the assumption
// internal/anthropic/messages relies on when it translates the kernel's
// cache_breakpoints into vendor cache_control markers: Anthropic has no
// implicit-caching model, so an adapter that silently dropped breakpoints
// would be a caching regression with no error anywhere.
func TestCaching_everyModelDeclaresExplicitMarkers(t *testing.T) {
	t.Parallel()

	for _, m := range Models() {
		if !m.Caching.Supported {
			t.Errorf("%s: caching is not declared supported", m.ID)
		}
		if !m.Caching.ExplicitMarkers {
			t.Errorf("%s: ExplicitMarkers = false, want true", m.ID)
		}
		if m.Caching.KeepaliveSupported {
			t.Errorf("%s: this plugin runs no keepalive loop, so the flag must be false", m.ID)
		}
	}
}

// TestSourcedOn_isAParseableDate keeps the staleness marker honest: a
// reader (or a future audit) comparing today's date against it needs it to
// actually be a date.
func TestSourcedOn_isAParseableDate(t *testing.T) {
	t.Parallel()

	if _, err := time.Parse(time.DateOnly, sourcedOn); err != nil {
		t.Fatalf("sourcedOn %q does not parse as a date: %v", sourcedOn, err)
	}
}

// findModel returns the roster entry with the given id, failing the test
// if the roster no longer carries it.
func findModel(t *testing.T, id string) model.Spec {
	t.Helper()
	for _, m := range Models() {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("roster has no model %q", id)
	return model.Spec{}
}

// contains reports whether haystack holds needle.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
