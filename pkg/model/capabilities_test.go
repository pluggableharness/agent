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
		Thinking:          model.ThinkingSpec{Mode: modelv1.ThinkingMode_THINKING_MODE_NONE},
		Caching:           model.CachingSpec{Mode: modelv1.CachingMode_CACHING_MODE_NONE},
		Pricing: model.Pricing{
			Currency: "USD",
			Tiers: []model.PricingTier{
				{InputPerMtok: 3, OutputPerMtok: 15},
			},
		},
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
			name: "discrete effort without effort levels",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = model.ThinkingSpec{
					Supported: true,
					Mode:      modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT,
					Default:   "medium",
				}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "continuous budget without budget range",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = model.ThinkingSpec{
					Supported: true,
					Mode:      modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET,
					Default:   "1024",
				}
				return []model.Spec{m}
			}(),
			schema:  &configv1.ConfigSchema{},
			wantErr: model.ErrInvalidCapabilities,
		},
		{
			name: "thinking mode set without default",
			models: func() []model.Spec {
				m := validModelSpec()
				m.Thinking = model.ThinkingSpec{
					Supported: true,
					Mode:      modelv1.ThinkingMode_THINKING_MODE_ALWAYS_ON_ADAPTIVE,
				}
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
				m.Caching = model.CachingSpec{Supported: true, Mode: modelv1.CachingMode_CACHING_MODE_EXPLICIT_MARKERS}
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
	m.Caching = model.CachingSpec{Supported: true, Mode: modelv1.CachingMode_CACHING_MODE_EXPLICIT_MARKERS}
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
