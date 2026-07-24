package agentprofile

import (
	"errors"
	"testing"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// capableSpec returns a ModelSpec that satisfies every TurnRequirements
// axis exercised in this file, for tests to selectively degrade.
func capableSpec() *modelv1.ModelSpec {
	return &modelv1.ModelSpec{
		Id:                "capable",
		ContextWindow:     200_000,
		SupportsToolUse:   true,
		SupportsVision:    true,
		SupportsStreaming: true,
		Thinking:          &modelv1.ThinkingSpec{Supported: true},
	}
}

func TestSelectModel_primaryEligible(t *testing.T) {
	t.Parallel()

	primary := ModelRef{Provider: "anthropic", ID: "claude-opus-4-8"}
	block := ModelBlock{
		Primary:   primary,
		Fallbacks: []ModelRef{{Provider: "anthropic", ID: "claude-sonnet-5"}},
	}
	specs := map[ModelRef]*modelv1.ModelSpec{
		primary: capableSpec(),
		{Provider: "anthropic", ID: "claude-sonnet-5"}: capableSpec(),
	}

	got, err := SelectModel(block, specs, TurnRequirements{NeedsToolUse: true})
	if err != nil {
		t.Fatalf("SelectModel: unexpected error: %v", err)
	}
	if got != primary {
		t.Errorf("SelectModel = %+v, want primary %+v", got, primary)
	}
}

func TestSelectModel_fallsThroughOnEachRequirementAxis(t *testing.T) {
	tests := []struct {
		name string
		req  TurnRequirements
		make func() *modelv1.ModelSpec // primary spec, ineligible on exactly one axis
	}{
		{
			name: "tool use",
			req:  TurnRequirements{NeedsToolUse: true},
			make: func() *modelv1.ModelSpec {
				s := capableSpec()
				s.SupportsToolUse = false
				return s
			},
		},
		{
			name: "vision",
			req:  TurnRequirements{NeedsVision: true},
			make: func() *modelv1.ModelSpec {
				s := capableSpec()
				s.SupportsVision = false
				return s
			},
		},
		{
			name: "thinking, spec present but unsupported",
			req:  TurnRequirements{NeedsThinking: true},
			make: func() *modelv1.ModelSpec {
				s := capableSpec()
				s.Thinking = &modelv1.ThinkingSpec{Supported: false}
				return s
			},
		},
		{
			name: "thinking, spec entirely absent",
			req:  TurnRequirements{NeedsThinking: true},
			make: func() *modelv1.ModelSpec {
				s := capableSpec()
				s.Thinking = nil
				return s
			},
		},
		{
			name: "context window too small",
			req:  TurnRequirements{MinContextWindow: 150_000},
			make: func() *modelv1.ModelSpec {
				s := capableSpec()
				s.ContextWindow = 100_000 // below the 150_000 requirement; fallback's 200_000 (capableSpec default) clears it
				return s
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			primary := ModelRef{Provider: "anthropic", ID: "primary"}
			fallback := ModelRef{Provider: "anthropic", ID: "fallback"}
			block := ModelBlock{Primary: primary, Fallbacks: []ModelRef{fallback}}
			specs := map[ModelRef]*modelv1.ModelSpec{
				primary:  tt.make(),
				fallback: capableSpec(),
			}

			got, err := SelectModel(block, specs, tt.req)
			if err != nil {
				t.Fatalf("SelectModel: unexpected error: %v", err)
			}
			if got != fallback {
				t.Errorf("SelectModel = %+v, want fallback %+v (primary should have been ineligible)", got, fallback)
			}
		})
	}
}

func TestSelectModel_entireChainIneligible(t *testing.T) {
	t.Parallel()

	primary := ModelRef{Provider: "anthropic", ID: "primary"}
	fallback := ModelRef{Provider: "anthropic", ID: "fallback"}
	block := ModelBlock{Primary: primary, Fallbacks: []ModelRef{fallback}}

	incapable := capableSpec()
	incapable.SupportsToolUse = false
	specs := map[ModelRef]*modelv1.ModelSpec{
		primary:  incapable,
		fallback: incapable,
	}

	_, err := SelectModel(block, specs, TurnRequirements{NeedsToolUse: true})
	if !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("SelectModel error = %v, want wrapping ErrNoEligibleModel", err)
	}
}

func TestSelectModel_missingFromSpecsIsSkippedNotError(t *testing.T) {
	t.Parallel()

	primary := ModelRef{Provider: "anthropic", ID: "not-loaded"}
	fallback := ModelRef{Provider: "anthropic", ID: "loaded"}
	block := ModelBlock{Primary: primary, Fallbacks: []ModelRef{fallback}}

	// primary has no entry in specs at all.
	specs := map[ModelRef]*modelv1.ModelSpec{
		fallback: capableSpec(),
	}

	got, err := SelectModel(block, specs, TurnRequirements{NeedsToolUse: true})
	if err != nil {
		t.Fatalf("SelectModel: unexpected error: %v", err)
	}
	if got != fallback {
		t.Errorf("SelectModel = %+v, want fallback %+v", got, fallback)
	}
}

func TestSelectModel_emptySpecsMap(t *testing.T) {
	t.Parallel()

	block := ModelBlock{Primary: ModelRef{Provider: "anthropic", ID: "x"}}
	_, err := SelectModel(block, map[ModelRef]*modelv1.ModelSpec{}, TurnRequirements{})
	if !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("SelectModel error = %v, want wrapping ErrNoEligibleModel", err)
	}
}
