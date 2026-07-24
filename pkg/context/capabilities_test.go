package context_test

import (
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	pluggablecontext "github.com/pluggableharness/agent/pkg/context"
)

func TestNewCapabilities_defaults(t *testing.T) {
	t.Parallel()

	schema := &configv1.ConfigSchema{}
	caps := pluggablecontext.NewCapabilities(2000, pluggablecontext.StabilityStatic, schema)

	if caps.DefaultTokenBudget != 2000 {
		t.Errorf("DefaultTokenBudget = %d, want 2000", caps.DefaultTokenBudget)
	}
	if caps.Stability != pluggablecontext.StabilityStatic {
		t.Errorf("Stability = %v, want StabilityStatic", caps.Stability)
	}
	if caps.Compactor {
		t.Error("Compactor = true, want false (default)")
	}
	if caps.ConfigSchema != schema {
		t.Errorf("ConfigSchema = %v, want %v", caps.ConfigSchema, schema)
	}
	if len(caps.SlashCommands) != 0 {
		t.Errorf("SlashCommands = %v, want empty", caps.SlashCommands)
	}
	if len(caps.SupportedHookPoints) != 0 {
		t.Errorf("SupportedHookPoints = %v, want empty", caps.SupportedHookPoints)
	}
}

func TestNewCapabilities_withOptions(t *testing.T) {
	t.Parallel()

	schema := &configv1.ConfigSchema{}
	specs := []*commonv1.PromptExpansionSpec{{Name: "review", Template: "review {{.arg}}"}}
	points := []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_SESSION_START, commonv1.HookPoint_HOOK_POINT_SESSION_END}

	caps := pluggablecontext.NewCapabilities(
		1500,
		pluggablecontext.StabilityDynamic,
		schema,
		pluggablecontext.WithCompactor(),
		pluggablecontext.WithSlashCommands(specs...),
		pluggablecontext.WithSupportedHookPoints(points...),
	)

	if !caps.Compactor {
		t.Error("Compactor = false, want true")
	}
	if len(caps.SlashCommands) != 1 || caps.SlashCommands[0].GetName() != "review" {
		t.Errorf("SlashCommands = %v, want [review]", caps.SlashCommands)
	}
	if len(caps.SupportedHookPoints) != 2 {
		t.Errorf("SupportedHookPoints = %v, want 2 entries", caps.SupportedHookPoints)
	}
}
