package model_test

import (
	"math"
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// optionsFrom builds a request carrying raw as provider_options.
func optionsFrom(t *testing.T, raw map[string]any) model.Options {
	t.Helper()
	s, err := structpb.NewStruct(raw)
	if err != nil {
		t.Fatalf("structpb.NewStruct(%v): %v", raw, err)
	}
	return model.ProviderOptions(&modelv1.StreamCompletionRequest{ProviderOptions: s})
}

func TestProviderOptions_absentSourcesAreEmptyNotPanicking(t *testing.T) {
	t.Parallel()

	// The zero value, a nil request, and a request with no provider_options
	// must all behave identically: a Provider reading an optional knob gets
	// one branch, not three.
	for name, opts := range map[string]model.Options{
		"zero value":         {},
		"nil request":        model.ProviderOptions(nil),
		"request without it": model.ProviderOptions(&modelv1.StreamCompletionRequest{}),
	} {
		if opts.Has("anything") {
			t.Errorf("%s: Has = true, want false", name)
		}
		if got := opts.Keys(); got != nil {
			t.Errorf("%s: Keys = %v, want nil", name, got)
		}
		if v, ok := opts.LookupString("anything"); ok || v != "" {
			t.Errorf("%s: LookupString = (%q, %v), want (\"\", false)", name, v, ok)
		}
	}
}

func TestProviderOptions_lookupsReturnTypedValues(t *testing.T) {
	t.Parallel()

	opts := optionsFrom(t, map[string]any{
		"tier":    "priority",
		"beta":    true,
		"seed":    float64(42),
		"top_p":   0.95,
		"nothing": nil,
	})

	if got, ok := opts.LookupString("tier"); !ok || got != "priority" {
		t.Errorf("LookupString(tier) = (%q, %v), want (priority, true)", got, ok)
	}
	if got, ok := opts.LookupBool("beta"); !ok || !got {
		t.Errorf("LookupBool(beta) = (%v, %v), want (true, true)", got, ok)
	}
	if got, ok := opts.LookupInt64("seed"); !ok || got != 42 {
		t.Errorf("LookupInt64(seed) = (%d, %v), want (42, true)", got, ok)
	}
	if got, ok := opts.LookupFloat64("top_p"); !ok || got != 0.95 {
		t.Errorf("LookupFloat64(top_p) = (%v, %v), want (0.95, true)", got, ok)
	}
}

func TestProviderOptions_wrongTypeReportsAbsentButHasStaysTrue(t *testing.T) {
	t.Parallel()

	// A wrong-typed value must fall back to the adapter's default rather
	// than becoming a zero value — but Has still reports the key, which is
	// how a Provider that wants to reject the config instead can tell the
	// two apart.
	opts := optionsFrom(t, map[string]any{"seed": "not-a-number"})

	if got, ok := opts.LookupInt64("seed"); ok || got != 0 {
		t.Errorf("LookupInt64 = (%d, %v), want (0, false)", got, ok)
	}
	if !opts.Has("seed") {
		t.Error("Has(seed) = false, want true — the key is present, only its type is wrong")
	}
}

func TestProviderOptions_lookupInt64RejectsInexactValues(t *testing.T) {
	t.Parallel()

	// Every case here would corrupt an operator's value if truncated.
	// 2^63 is the specific trap: float64(math.MaxInt64) rounds UP to 2^63,
	// so a naive `f > float64(math.MaxInt64)` bound lets it through and the
	// int64 conversion overflows.
	const twoPow63 = float64(1 << 63)
	for name, v := range map[string]float64{
		"fractional":   1.5,
		"NaN":          math.NaN(),
		"+Inf":         math.Inf(1),
		"-Inf":         math.Inf(-1),
		"exactly 2^63": twoPow63,
		"above 2^63":   twoPow63 * 2,
		"below -2^63":  -twoPow63 * 2,
	} {
		opts := optionsFrom(t, map[string]any{"n": v})
		if got, ok := opts.LookupInt64("n"); ok {
			t.Errorf("LookupInt64(%s = %v) = (%d, true), want ok=false", name, v, got)
		}
	}

	// The largest value that IS exactly representable must still pass, so
	// the bound above isn't rejecting legitimate input.
	opts := optionsFrom(t, map[string]any{"n": -twoPow63})
	if got, ok := opts.LookupInt64("n"); !ok || got != math.MinInt64 {
		t.Errorf("LookupInt64(-2^63) = (%d, %v), want (%d, true)", got, ok, int64(math.MinInt64))
	}
}

func TestProviderOptions_keysAreSorted(t *testing.T) {
	t.Parallel()

	// Sorted so a provider logging or erroring with these produces
	// identical output across runs (determinism.md's serialization rule).
	opts := optionsFrom(t, map[string]any{"zeta": 1.0, "alpha": 2.0, "mu": 3.0})

	want := []string{"alpha", "mu", "zeta"}
	if got := opts.Keys(); !slices.Equal(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}
}
