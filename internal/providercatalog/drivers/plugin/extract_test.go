package plugin

import (
	"slices"
	"testing"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/pluginhost"
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

// TestDedupeSchemas covers a tool provider advertising one operation name
// more than once. Before dedupeSchemas existed, each duplicate got its own
// goroutine writing the same toolKey, so which schema landed in the
// catalog depended on goroutine completion order — nondeterministic
// between runs of the same session.
//
// The kind/risk assertion is the part that matters: those two fields are
// what internal/policy.Evaluate classifies a call by, so a
// nondeterministic winner meant a call that was a plain data_source read
// on one run and a gated resource mutation on the next.
func TestDedupeSchemas(t *testing.T) {
	t.Parallel()

	first := &toolv1.ToolSchema{
		Name: "write",
		Kind: toolv1.ToolKind_TOOL_KIND_RESOURCE,
		Risk: toolv1.RiskClass_RISK_CLASS_CRITICAL,
	}
	shadowing := &toolv1.ToolSchema{
		Name: "write",
		Kind: toolv1.ToolKind_TOOL_KIND_DATA_SOURCE,
		Risk: toolv1.RiskClass_RISK_CLASS_READ_ONLY,
	}
	other := &toolv1.ToolSchema{Name: "read", Kind: toolv1.ToolKind_TOOL_KIND_DATA_SOURCE}

	got := dedupeSchemas(t.Context(), testLogger(), "fs", []*toolv1.ToolSchema{first, shadowing, other})

	if len(got) != 2 {
		t.Fatalf("dedupeSchemas returned %d schemas, want 2", len(got))
	}
	if got[0] != first {
		t.Errorf("first occurrence must win; got %+v", got[0])
	}
	if got[0].GetKind() != toolv1.ToolKind_TOOL_KIND_RESOURCE || got[0].GetRisk() != toolv1.RiskClass_RISK_CLASS_CRITICAL {
		t.Errorf("the surviving schema's kind/risk = %v/%v, want RESOURCE/CRITICAL — "+
			"a shadowing duplicate must never be able to downgrade how policy classifies the operation",
			got[0].GetKind(), got[0].GetRisk())
	}
	if got[1] != other {
		t.Errorf("non-duplicate schema was dropped or reordered; got %+v", got[1])
	}
}

// TestDedupeSchemas_noDuplicatesIsIdentity pins that the ordinary case is
// untouched: same schemas, same order, nothing dropped.
func TestDedupeSchemas_noDuplicatesIsIdentity(t *testing.T) {
	t.Parallel()

	in := []*toolv1.ToolSchema{
		{Name: "read"},
		{Name: "write"},
		{Name: "list"},
	}

	got := dedupeSchemas(t.Context(), testLogger(), "fs", in)

	if len(got) != len(in) {
		t.Fatalf("dedupeSchemas returned %d schemas, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("schema %d = %+v, want %+v (order must be preserved)", i, got[i], in[i])
		}
	}
}

// TestBuildModels_aliasesResolveToTheCanonicalHandle asserts a model
// published with aliases is reachable under every one of them, and that
// each alias keeps the canonical Ref.
//
// The Ref matters as much as the reachability: it is what the kernel
// bills, logs, and records against, so an alias minting its own identity
// would split one model's cost and history across several names — the
// duplicate-model problem CatalogMetadata.aliases exists to retire.
func TestBuildModels_aliasesResolveToTheCanonicalHandle(t *testing.T) {
	t.Parallel()

	reg := pluginhost.NewRegistry()
	if err := reg.Add(live("xai", commonv1.Category_CATEGORY_MODEL, "xai", 0, nilModelClient(),
		&modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{Models: []*modelv1.ModelSpec{{
			Id:            "grok-4.3",
			ContextWindow: 256_000,
			Catalog:       &modelv1.CatalogMetadata{Aliases: []string{"grok-4", "grok-latest"}},
		}}}})); err != nil {
		t.Fatalf("Add: %v", err)
	}

	models := buildModels(t.Context(), testLogger(), reg)

	canonical := agentprofile.ModelRef{Provider: "xai", ID: "grok-4.3"}
	for _, id := range []string{"grok-4.3", "grok-4", "grok-latest"} {
		got, ok := models[agentprofile.ModelRef{Provider: "xai", ID: id}]
		if !ok {
			t.Errorf("model %q is not reachable", id)
			continue
		}
		if got.Ref != canonical {
			t.Errorf("model %q: Ref = %+v, want the canonical %+v", id, got.Ref, canonical)
		}
		if got.Spec.GetId() != "grok-4.3" {
			t.Errorf("model %q: Spec.Id = %q, want grok-4.3", id, got.Spec.GetId())
		}
	}
}

// TestBuildModels_aRealIDBeatsAnAlias covers the collision: when one
// model's alias is another model's real id, the real model wins. Order
// must not decide it, so both orderings are asserted.
func TestBuildModels_aRealIDBeatsAnAlias(t *testing.T) {
	t.Parallel()

	aliasing := &modelv1.ModelSpec{
		Id:      "big",
		Catalog: &modelv1.CatalogMetadata{Aliases: []string{"small"}},
	}
	realModel := &modelv1.ModelSpec{Id: "small"}

	for _, tc := range []struct {
		name  string
		specs []*modelv1.ModelSpec
	}{
		{"alias declared first", []*modelv1.ModelSpec{aliasing, realModel}},
		{"real model declared first", []*modelv1.ModelSpec{realModel, aliasing}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := pluginhost.NewRegistry()
			if err := reg.Add(live("v", commonv1.Category_CATEGORY_MODEL, "v", 0, nilModelClient(),
				&modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{Models: tc.specs}})); err != nil {
				t.Fatalf("Add: %v", err)
			}

			models := buildModels(t.Context(), testLogger(), reg)

			got, ok := models[agentprofile.ModelRef{Provider: "v", ID: "small"}]
			if !ok {
				t.Fatal(`model "small" is not reachable`)
			}
			if got.Spec.GetId() != "small" {
				t.Errorf(`"small" resolved to %q, want the real model regardless of declaration order`, got.Spec.GetId())
			}
		})
	}
}
