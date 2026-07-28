package frontend_test

import (
	"reflect"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/frontend"
)

func TestNewCapabilities(t *testing.T) {
	t.Parallel()

	attr, err := config.Attribute("theme", configv1.AttrType_ATTR_TYPE_STRING)
	if err != nil {
		t.Fatalf("config.Attribute() error = %v", err)
	}
	schema, err := config.Schema(attr)
	if err != nil {
		t.Fatalf("config.Schema() error = %v", err)
	}

	slash := &commonv1.PromptExpansionSpec{Name: "explain"}

	caps := frontend.NewCapabilities(schema,
		frontend.WithSlashCommands(slash),
		frontend.WithSupportedHookPoints(commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL),
	)

	if caps.ConfigSchema != schema {
		t.Errorf("ConfigSchema = %v, want %v", caps.ConfigSchema, schema)
	}
	if len(caps.SlashCommands) != 1 || caps.SlashCommands[0] != slash {
		t.Errorf("SlashCommands = %v, want [%v]", caps.SlashCommands, slash)
	}
	wantHooks := []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL}
	if !reflect.DeepEqual(caps.SupportedHookPoints, wantHooks) {
		t.Errorf("SupportedHookPoints = %v, want %v", caps.SupportedHookPoints, wantHooks)
	}
}

func TestNewCapabilities_NilSchema(t *testing.T) {
	t.Parallel()

	caps := frontend.NewCapabilities(nil)
	if caps.ConfigSchema != nil {
		t.Errorf("ConfigSchema = %v, want nil", caps.ConfigSchema)
	}
	if caps.SlashCommands != nil {
		t.Errorf("SlashCommands = %v, want nil", caps.SlashCommands)
	}
}
