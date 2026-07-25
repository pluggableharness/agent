package modelrequest

import (
	"testing"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func strPtr(s string) *string { return &s }
func i64Ptr(v int64) *int64   { return &v }

func discreteEffortSpec(levels ...string) *modelv1.ModelSpec {
	return &modelv1.ModelSpec{
		Thinking: &modelv1.ThinkingSpec{
			Supported:    true,
			Mode:         modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT,
			EffortLevels: levels,
		},
	}
}

func continuousBudgetSpec(lo, hi int64) *modelv1.ModelSpec {
	return &modelv1.ModelSpec{
		Thinking: &modelv1.ThinkingSpec{
			Supported:   true,
			Mode:        modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET,
			BudgetRange: &modelv1.ThinkingBudgetRange{Min: lo, Max: hi},
		},
	}
}

func toolChoiceSpec(modes ...modelv1.ToolChoiceMode) *modelv1.ModelSpec {
	return &modelv1.ModelSpec{SupportedToolChoiceModes: modes}
}

func TestValidateParamsNilReq(t *testing.T) {
	t.Parallel()

	got := ValidateParams(nil, discreteEffortSpec("low", "high"))
	if got.Resolved != nil {
		t.Fatalf("Resolved = %v, want nil", got.Resolved)
	}
	if got.FellBackThinking || got.FellBackToolChoice {
		t.Fatalf("got fallback flags %+v, want both false", got)
	}
}

func TestValidateParamsThinkingEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spec       *modelv1.ModelSpec
		effort     string
		wantEffort string // "" means cleared
		wantFallen bool
	}{
		{
			name:       "in range",
			spec:       discreteEffortSpec("low", "medium", "high"),
			effort:     "high",
			wantEffort: "high",
			wantFallen: false,
		},
		{
			name:       "out of range",
			spec:       discreteEffortSpec("low", "medium", "high"),
			effort:     "ultra",
			wantEffort: "",
			wantFallen: true,
		},
		{
			name:       "mode mismatch falls back even for a plausible-looking value",
			spec:       continuousBudgetSpec(1024, 32000),
			effort:     "high",
			wantEffort: "",
			wantFallen: true,
		},
		{
			name:       "thinking unsupported at all falls back",
			spec:       &modelv1.ModelSpec{Thinking: &modelv1.ThinkingSpec{Supported: false, Mode: modelv1.ThinkingMode_THINKING_MODE_NONE}},
			effort:     "low",
			wantEffort: "",
			wantFallen: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &modelv1.GenerationParams{ThinkingEffort: strPtr(tt.effort)}
			got := ValidateParams(req, tt.spec)

			if got.FellBackThinking != tt.wantFallen {
				t.Fatalf("FellBackThinking = %v, want %v", got.FellBackThinking, tt.wantFallen)
			}
			if tt.wantEffort == "" {
				if got.Resolved.ThinkingEffort != nil {
					t.Fatalf("ThinkingEffort = %q, want cleared", got.Resolved.GetThinkingEffort())
				}
			} else if got.Resolved.GetThinkingEffort() != tt.wantEffort {
				t.Fatalf("ThinkingEffort = %q, want %q", got.Resolved.GetThinkingEffort(), tt.wantEffort)
			}
		})
	}
}

func TestValidateParamsThinkingBudget(t *testing.T) {
	t.Parallel()

	spec := continuousBudgetSpec(1024, 32000)

	tests := []struct {
		name       string
		budget     int64
		wantFallen bool
	}{
		{name: "within range", budget: 5000, wantFallen: false},
		{name: "at min bound (inclusive)", budget: 1024, wantFallen: false},
		{name: "at max bound (inclusive)", budget: 32000, wantFallen: false},
		{name: "below min", budget: 1023, wantFallen: true},
		{name: "above max", budget: 32001, wantFallen: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &modelv1.GenerationParams{ThinkingBudgetTokens: i64Ptr(tt.budget)}
			got := ValidateParams(req, spec)

			if got.FellBackThinking != tt.wantFallen {
				t.Fatalf("FellBackThinking = %v, want %v", got.FellBackThinking, tt.wantFallen)
			}
			if tt.wantFallen {
				if got.Resolved.ThinkingBudgetTokens != nil {
					t.Fatalf("ThinkingBudgetTokens = %v, want cleared", got.Resolved.GetThinkingBudgetTokens())
				}
			} else if got.Resolved.GetThinkingBudgetTokens() != tt.budget {
				t.Fatalf("ThinkingBudgetTokens = %v, want %v", got.Resolved.GetThinkingBudgetTokens(), tt.budget)
			}
		})
	}
}

func TestValidateParamsThinkingBudgetModeMismatch(t *testing.T) {
	t.Parallel()

	// A numerically plausible budget still falls back when the resolved
	// model's ThinkingSpec.mode isn't THINKING_MODE_CONTINUOUS_BUDGET —
	// mirrors the effort-side "mode mismatch" case above.
	spec := discreteEffortSpec("low", "high")
	req := &modelv1.GenerationParams{ThinkingBudgetTokens: i64Ptr(5000)}

	got := ValidateParams(req, spec)

	if !got.FellBackThinking {
		t.Fatalf("FellBackThinking = false, want true")
	}
	if got.Resolved.ThinkingBudgetTokens != nil {
		t.Fatalf("ThinkingBudgetTokens = %v, want cleared", got.Resolved.GetThinkingBudgetTokens())
	}
}

func TestValidateParamsThinkingBudgetMissingRange(t *testing.T) {
	t.Parallel()

	// A ThinkingSpec claiming CONTINUOUS_BUDGET mode but omitting
	// BudgetRange (a malformed capability declaration) must still fall
	// back rather than panic.
	spec := &modelv1.ModelSpec{
		Thinking: &modelv1.ThinkingSpec{Supported: true, Mode: modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET},
	}
	req := &modelv1.GenerationParams{ThinkingBudgetTokens: i64Ptr(5000)}
	got := ValidateParams(req, spec)

	if !got.FellBackThinking {
		t.Fatalf("FellBackThinking = false, want true")
	}
	if got.Resolved.ThinkingBudgetTokens != nil {
		t.Fatalf("ThinkingBudgetTokens = %v, want cleared", got.Resolved.GetThinkingBudgetTokens())
	}
}

func TestValidateParamsToolChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spec       *modelv1.ModelSpec
		mode       modelv1.ToolChoiceMode
		wantFallen bool
		wantMode   modelv1.ToolChoiceMode // meaningless if wantFallen
	}{
		{
			name:       "supported mode forwarded as-is",
			spec:       toolChoiceSpec(modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY, modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC),
			mode:       modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY,
			wantFallen: false,
			wantMode:   modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY,
		},
		{
			name:       "unsupported mode falls back to AUTO",
			spec:       toolChoiceSpec(modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY),
			mode:       modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_NONE,
			wantFallen: true,
		},
		{
			name:       "empty supported list rejects everything but AUTO",
			spec:       toolChoiceSpec(),
			mode:       modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC,
			wantFallen: true,
		},
		{
			name:       "AUTO always allowed even when absent from the declared list",
			spec:       toolChoiceSpec(modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY),
			mode:       modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO,
			wantFallen: false,
			wantMode:   modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO,
		},
		{
			name:       "unspecified zero-value mode falls back",
			spec:       toolChoiceSpec(modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY),
			mode:       modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_UNSPECIFIED,
			wantFallen: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &modelv1.GenerationParams{ToolChoice: &modelv1.ToolChoice{Mode: tt.mode}}
			got := ValidateParams(req, tt.spec)

			if got.FellBackToolChoice != tt.wantFallen {
				t.Fatalf("FellBackToolChoice = %v, want %v", got.FellBackToolChoice, tt.wantFallen)
			}
			if tt.wantFallen {
				if got.Resolved.ToolChoice != nil {
					t.Fatalf("ToolChoice = %v, want cleared", got.Resolved.ToolChoice)
				}
			} else if got.Resolved.GetToolChoice().GetMode() != tt.wantMode {
				t.Fatalf("ToolChoice.Mode = %v, want %v", got.Resolved.GetToolChoice().GetMode(), tt.wantMode)
			}
		})
	}
}

func TestValidateParamsBothFallBackSimultaneously(t *testing.T) {
	t.Parallel()

	spec := discreteEffortSpec("low", "high")
	// spec declares no supported_tool_choice_modes at all.
	req := &modelv1.GenerationParams{
		ThinkingEffort: strPtr("ultra"),
		ToolChoice:     &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC, ToolName: strPtr("delete_repo")},
	}

	got := ValidateParams(req, spec)

	if !got.FellBackThinking {
		t.Fatalf("FellBackThinking = false, want true")
	}
	if !got.FellBackToolChoice {
		t.Fatalf("FellBackToolChoice = false, want true")
	}
	if got.Resolved.ThinkingEffort != nil {
		t.Fatalf("ThinkingEffort = %q, want cleared", got.Resolved.GetThinkingEffort())
	}
	if got.Resolved.ToolChoice != nil {
		t.Fatalf("ToolChoice = %v, want cleared", got.Resolved.ToolChoice)
	}
}

func TestValidateParamsNeitherSetIsNoOp(t *testing.T) {
	t.Parallel()

	spec := discreteEffortSpec("low", "high")
	req := &modelv1.GenerationParams{
		MaxOutputTokens: i64Ptr(4096),
		Temperature:     ptrF64(0.7),
		StopSequences:   []string{"STOP"},
	}

	got := ValidateParams(req, spec)

	if got.FellBackThinking || got.FellBackToolChoice {
		t.Fatalf("got fallback flags %+v, want both false", got)
	}
	if got.Resolved.GetMaxOutputTokens() != 4096 {
		t.Fatalf("MaxOutputTokens = %v, want 4096", got.Resolved.GetMaxOutputTokens())
	}
	if got.Resolved.GetTemperature() != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", got.Resolved.GetTemperature())
	}
	if len(got.Resolved.GetStopSequences()) != 1 || got.Resolved.GetStopSequences()[0] != "STOP" {
		t.Fatalf("StopSequences = %v, want [STOP]", got.Resolved.GetStopSequences())
	}
}

func TestValidateParamsDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	spec := discreteEffortSpec("low", "high")
	req := &modelv1.GenerationParams{
		ThinkingEffort: strPtr("ultra"), // out of range, will fall back
		ToolChoice:     &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO},
	}

	got := ValidateParams(req, spec)

	if req.GetThinkingEffort() != "ultra" {
		t.Fatalf("caller's req.ThinkingEffort was mutated: got %q", req.GetThinkingEffort())
	}
	if req.ToolChoice == nil {
		t.Fatalf("caller's req.ToolChoice was mutated to nil")
	}
	if got.Resolved == req {
		t.Fatalf("Resolved aliases the caller's req pointer, want a clone")
	}
}

func ptrF64(v float64) *float64 { return &v }
