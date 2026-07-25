package plugin

import (
	"slices"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// TestSupportedHookPoints confirms every one of the seven plugin
// categories' capability response carries SupportedHookPoints
// somewhere, at whatever nesting depth that category's proto uses —
// doc.go's "resolving SupportsPreview" sibling finding for hooks: all
// seven were confirmed by reading pkg/<category>/proto/v1 directly, not
// assumed. Also covers a category/capabilities-type mismatch (the
// defensive nil every other extractor in this file falls back to) and
// the unspecified category.
func TestSupportedHookPoints(t *testing.T) {
	t.Parallel()

	preToolCall := commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL
	sessionStart := commonv1.HookPoint_HOOK_POINT_SESSION_START

	tests := []struct {
		name         string
		category     commonv1.Category
		capabilities any
		want         []commonv1.HookPoint
	}{
		{
			name:     "model",
			category: commonv1.Category_CATEGORY_MODEL,
			capabilities: &modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{
				Models:              []*modelv1.ModelSpec{{Id: "m"}},
				SupportedHookPoints: []commonv1.HookPoint{preToolCall},
			}},
			want: []commonv1.HookPoint{preToolCall},
		},
		{
			name:     "tool (flat on the response, not nested)",
			category: commonv1.Category_CATEGORY_TOOL,
			capabilities: &toolv1.GetSchemaResponse{
				SupportedHookPoints: []commonv1.HookPoint{preToolCall, sessionStart},
			},
			want: []commonv1.HookPoint{preToolCall, sessionStart},
		},
		{
			name:     "context",
			category: commonv1.Category_CATEGORY_CONTEXT,
			capabilities: &contextv1.GetCapabilitiesResponse{Capabilities: &contextv1.ContextCapabilities{
				SupportedHookPoints: []commonv1.HookPoint{sessionStart},
			}},
			want: []commonv1.HookPoint{sessionStart},
		},
		{
			name:         "memory",
			category:     commonv1.Category_CATEGORY_MEMORY,
			capabilities: memoryCapabilities(sessionStart),
			want:         []commonv1.HookPoint{sessionStart},
		},
		{
			name:         "frontend",
			category:     commonv1.Category_CATEGORY_FRONTEND,
			capabilities: frontendCapabilities(preToolCall),
			want:         []commonv1.HookPoint{preToolCall},
		},
		{
			name:         "widget",
			category:     commonv1.Category_CATEGORY_WIDGET,
			capabilities: widgetCapabilities(preToolCall, sessionStart),
			want:         []commonv1.HookPoint{preToolCall, sessionStart},
		},
		{
			name:         "slashcommand (flat on the response, not nested)",
			category:     commonv1.Category_CATEGORY_SLASHCOMMAND,
			capabilities: slashcommandCapabilities(sessionStart),
			want:         []commonv1.HookPoint{sessionStart},
		},
		{
			name:         "unspecified category",
			category:     commonv1.Category_CATEGORY_UNSPECIFIED,
			capabilities: memoryCapabilities(sessionStart),
			want:         nil,
		},
		{
			name:         "capabilities value of the wrong Go type for its category",
			category:     commonv1.Category_CATEGORY_MODEL,
			capabilities: &toolv1.GetSchemaResponse{SupportedHookPoints: []commonv1.HookPoint{preToolCall}},
			want:         nil,
		},
		{
			name:         "nil capabilities",
			category:     commonv1.Category_CATEGORY_MODEL,
			capabilities: nil,
			want:         nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := supportedHookPoints(tt.category, tt.capabilities)
			if !slices.Equal(got, tt.want) {
				t.Errorf("supportedHookPoints(%v, ...) = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}
