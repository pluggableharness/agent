package widget_test

import (
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	"github.com/pluggableharness/agent/pkg/widget"
)

func TestNewCapabilities(t *testing.T) {
	t.Parallel()

	attr, err := config.Attribute("enabled", configv1.AttrType_ATTR_TYPE_BOOL)
	if err != nil {
		t.Fatalf("config.Attribute: %v", err)
	}
	schema, err := config.Schema(attr)
	if err != nil {
		t.Fatalf("config.Schema: %v", err)
	}
	regions := []renderv1.Region{renderv1.Region_REGION_SIDEBAR, renderv1.Region_REGION_TOP_BAR}

	got := widget.NewCapabilities(schema, regions, commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, commonv1.HookPoint_HOOK_POINT_SESSION_START)

	if got.ConfigSchema != schema {
		t.Errorf("NewCapabilities().ConfigSchema = %p, want %p", got.ConfigSchema, schema)
	}
	if len(got.Regions) != 2 || got.Regions[0] != renderv1.Region_REGION_SIDEBAR || got.Regions[1] != renderv1.Region_REGION_TOP_BAR {
		t.Errorf("NewCapabilities().Regions = %v, want %v", got.Regions, regions)
	}
	wantHooks := []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL, commonv1.HookPoint_HOOK_POINT_SESSION_START}
	if len(got.SupportedHookPoints) != 2 || got.SupportedHookPoints[0] != wantHooks[0] || got.SupportedHookPoints[1] != wantHooks[1] {
		t.Errorf("NewCapabilities().SupportedHookPoints = %v, want %v", got.SupportedHookPoints, wantHooks)
	}
}

func TestNewCapabilities_noHookPoints(t *testing.T) {
	t.Parallel()

	got := widget.NewCapabilities(nil, []renderv1.Region{renderv1.Region_REGION_OVERLAY})

	if got.SupportedHookPoints != nil {
		t.Errorf("NewCapabilities().SupportedHookPoints = %v, want nil", got.SupportedHookPoints)
	}
	if got.ConfigSchema != nil {
		t.Errorf("NewCapabilities().ConfigSchema = %v, want nil", got.ConfigSchema)
	}
}
