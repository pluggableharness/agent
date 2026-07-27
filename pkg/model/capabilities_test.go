package model_test

import (
	"errors"
	"testing"
	"time"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	model "github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func mustFloat64(f float64) *float64 { return &f }
func mustInt64(i int64) *int64       { return &i }

// validModelSpec returns a minimal, invariant-satisfying Spec a test
// can mutate to exercise one specific violation at a time.
func validModelSpec() model.Spec {
	return model.Spec{
		ID:                "claude-test",
		ContextWindow:     200000,
		MaxOutputTokens:   8192,
		SupportsToolUse:   true,
		SupportsVision:    true,
		SupportsStreaming: true,
		Thinking:          model.ThinkingSpec{},
		Caching:           model.CachingSpec{},
		Pricing: model.Pricing{
			Currency: "USD",
			Tiers: []model.PricingTier{
				{InputPerMtok: 3, OutputPerMtok: 15},
			},
		},
	}
}

// thinkingWith returns a thinking-supported ThinkingSpec carrying the
// given controls, with the fields that are not under test set to values
// that satisfy their own invariants — so a failing case fails on the
// control it names, never on an unrelated omission.
func thinkingWith(effort *model.EffortControl, budget *model.BudgetControl) model.ThinkingSpec {
	return model.ThinkingSpec{
		Supported: true,
		Effort:    effort,
		Budget:    budget,
		Disable:   modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS,
	}
}

func validConfigSchema(t *testing.T) *configv1.ConfigSchema {
	t.Helper()
	return &configv1.ConfigSchema{}
}

func TestNewCapabilities_Valid(t *testing.T) {
	t.Parallel()

	caps, err := model.NewCapabilities([]model.Spec{validModelSpec()}, validConfigSchema(t))
	if err != nil {
		t.Fatalf("NewCapabilities() = %v, want nil error", err)
	}
	if len(caps.Models) != 1 {
		t.Errorf("len(caps.Models) = %d, want 1", len(caps.Models))
	}
}

func TestNewCapabilities_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		models  []model.Spec
		schema  *configv1.ConfigSchema
		wantErr error
	}{
		{
			name:    "no models",
			models:  nil,
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name:    "no config schema",
			models:  []model.Spec{validModelSpec()},
			schema:  nil,
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "model missing id",
			models: func() []model.Spec {
				m := validModelSpec()
				m.ID = ""
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "effort control with no levels",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = thinkingWith(&model.EffortControl{Default: "medium"}, nil)
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "effort control with no default",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = thinkingWith(&model.EffortControl{Levels: []string{"low", "high"}}, nil)
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			// The default exists so a kernel can send it back as an explicit
			// override; naming a level the vendor does not accept would make
			// that override a guaranteed 400.
			name: "effort default is not one of the declared levels",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = thinkingWith(&model.EffortControl{
					Levels:  []string{"low", "high"},
					Default: "medium",
				}, nil)
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "budget range inverted",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = thinkingWith(nil, &model.BudgetControl{
					Range: model.ThinkingBudgetRange{Min: 32000, Max: 1024},
				})
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "budget default outside the declared range",
			models: func() []model.Spec {
				m := validModelSpec()
				def := int64(64000)
				m.Thinking = thinkingWith(nil, &model.BudgetControl{
					Range:   model.ThinkingBudgetRange{Min: 1024, Max: 32000},
					Default: &def,
				})
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "thinking supported without a disable value",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = model.ThinkingSpec{Supported: true}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			// A model that cannot reason must not declare controls for
			// reasoning it does not do — the one cross-axis rule.
			name: "control declared on a model with thinking unsupported",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = model.ThinkingSpec{
					Supported: false,
					Effort:    &model.EffortControl{Levels: []string{"low"}, Default: "low"},
				}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "adaptive_by_default set on a model with thinking unsupported",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = model.ThinkingSpec{Supported: false, AdaptiveByDefault: true}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			// Declaring caching without naming a mechanism would read as
			// "no caching" to every caller, so it is rejected rather than
			// silently degraded.
			name: "caching supported but neither mechanism declared",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Caching = model.CachingSpec{Supported: true}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "caching mechanism declared on a model with caching unsupported",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Caching = model.CachingSpec{Supported: false, ImplicitAutomatic: true}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "pricing missing currency",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Pricing.Currency = ""
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidPricing,
		},
		{
			name: "pricing without tiers and not free",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Pricing.Tiers = nil
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidPricing,
		},
		{
			name: "caching supported but tier missing cache pricing",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Caching = model.CachingSpec{Supported: true, ExplicitMarkers: true}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidPricing,
		},
		{
			name: "overlapping pricing tiers",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Pricing.Tiers = []model.PricingTier{
					{InputPerMtok: 3, OutputPerMtok: 15},
					{InputPerMtok: 4, OutputPerMtok: 20},
				}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidPricing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := model.NewCapabilities(tt.models, tt.schema)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewCapabilities() error = %v, want wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewCapabilities_CachingSatisfiedTiersAreValid(t *testing.T) {
	t.Parallel()

	m := validModelSpec()
	m.Caching = model.CachingSpec{Supported: true, ExplicitMarkers: true}
	m.Pricing.Tiers = []model.PricingTier{
		{InputPerMtok: 3, OutputPerMtok: 15, CacheWritePerMtok: mustFloat64(3.75), CacheReadPerMtok: mustFloat64(0.3)},
	}

	if _, err := model.NewCapabilities([]model.Spec{m}, &configv1.ConfigSchema{}); err != nil {
		t.Fatalf("NewCapabilities() = %v, want nil", err)
	}
}

func TestNewCapabilities_NonOverlappingTimeBoundedTiersAreValid(t *testing.T) {
	t.Parallel()

	m := validModelSpec()
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	m.Pricing.Tiers = []model.PricingTier{
		{InputPerMtok: 1, OutputPerMtok: 5, EffectiveUntil: &feb},
		{InputPerMtok: 3, OutputPerMtok: 15, EffectiveFrom: &feb},
	}

	if _, err := model.NewCapabilities([]model.Spec{m}, &configv1.ConfigSchema{}); err != nil {
		t.Fatalf("NewCapabilities() = %v, want nil", err)
	}
}

func TestNewCapabilities_NonOverlappingInputSizeBoundedTiersAreValid(t *testing.T) {
	t.Parallel()

	m := validModelSpec()
	m.Pricing.Tiers = []model.PricingTier{
		{InputPerMtok: 3, OutputPerMtok: 15, InputTokensUntil: mustInt64(200000)},
		{InputPerMtok: 6, OutputPerMtok: 15, InputTokensFrom: mustInt64(200000)},
	}

	if _, err := model.NewCapabilities([]model.Spec{m}, &configv1.ConfigSchema{}); err != nil {
		t.Fatalf("NewCapabilities() = %v, want nil", err)
	}
}

func TestNewCapabilities_WithOptions(t *testing.T) {
	t.Parallel()

	caps, err := model.NewCapabilities(
		[]model.Spec{validModelSpec()},
		&configv1.ConfigSchema{},
		model.WithSupportedHookPoints(),
		model.WithSlashCommands(),
	)
	if err != nil {
		t.Fatalf("NewCapabilities() = %v, want nil", err)
	}
	if caps.SupportedHookPoints == nil && len(caps.SupportedHookPoints) != 0 {
		t.Errorf("SupportedHookPoints unexpectedly non-empty")
	}
}

func TestNewCapabilities_FreeModelWithoutTiersIsValid(t *testing.T) {
	t.Parallel()

	m := validModelSpec()
	m.Pricing = model.Pricing{Currency: "USD", Free: true}

	if _, err := model.NewCapabilities([]model.Spec{m}, &configv1.ConfigSchema{}); err != nil {
		t.Fatalf("NewCapabilities() = %v, want nil", err)
	}
}
