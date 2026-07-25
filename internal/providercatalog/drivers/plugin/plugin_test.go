package plugin

import (
	"errors"
	"maps"
	"slices"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/pluginhost"
	"github.com/pluggableharness/agent/internal/providercatalog"
)

// buildTestCatalog assembles a *pluginhost.Registry standing in for a
// realistic multi-provider session and builds a Catalog over it:
//
//   - two model-category providers ("anthropic" and "other-vendor"),
//     exercising ModelSpecs' cross-provider aggregation
//   - two tool-category providers ("fs", "shell"), covering both
//     SupportsPreview outcomes and TerminatesTurn
//   - two context-category providers ("claude-md", "git-status"),
//     declared out of launch order so Contexts' Position-ordering
//     contract is actually exercised
//   - one widget-category provider ("sidebar") with no model/tool/context
//     relevance at all, present solely to prove a wrong-category
//     provider never corrupts another category's lookups
func buildTestCatalog(t *testing.T) *Catalog {
	t.Helper()

	reg := pluginhost.NewRegistry()
	adds := []*pluginhost.Live{
		live("anthropic", commonv1.Category_CATEGORY_MODEL, "claude", 0, nilModelClient(),
			&modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{Models: []*modelv1.ModelSpec{
				{Id: "claude-opus-4", ContextWindow: 200_000},
				{Id: "claude-haiku-4", ContextWindow: 200_000, SupportsToolUse: true},
			}}}),
		live("other-vendor", commonv1.Category_CATEGORY_MODEL, "small-vendor", 1, nilModelClient(),
			&modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{Models: []*modelv1.ModelSpec{
				{Id: "small-model", ContextWindow: 32_000},
			}}}),
		live("fs", commonv1.Category_CATEGORY_TOOL, "filesystem", 2, previewOK(),
			&toolv1.GetSchemaResponse{Tools: []*toolv1.ToolSchema{
				{Name: "read_file", Kind: toolv1.ToolKind_TOOL_KIND_DATA_SOURCE},
			}}),
		live("shell", commonv1.Category_CATEGORY_TOOL, "shell-exec", 3, previewInvalidArgument(),
			&toolv1.GetSchemaResponse{Tools: []*toolv1.ToolSchema{
				{Name: "run", Kind: toolv1.ToolKind_TOOL_KIND_RESOURCE, TerminatesTurn: true},
			}}),
		// Declared after "shell" in the registry, but LaunchIndex 5 sorts
		// after "claude-md" (LaunchIndex 4) — Contexts must return
		// claude-md before git-status regardless of Add order.
		live("git-status", commonv1.Category_CATEGORY_CONTEXT, "git", 5, nilContextClient(),
			&contextv1.GetCapabilitiesResponse{Capabilities: &contextv1.ContextCapabilities{DefaultTokenBudget: 2000}}),
		live("claude-md", commonv1.Category_CATEGORY_CONTEXT, "md-reader", 4, nilContextClient(),
			&contextv1.GetCapabilitiesResponse{Capabilities: &contextv1.ContextCapabilities{DefaultTokenBudget: 4000, Compactor: true}}),
		live("sidebar", commonv1.Category_CATEGORY_WIDGET, "widget-x", 6, nil,
			&widgetv1.GetCapabilitiesResponse{Capabilities: &widgetv1.WidgetCapabilities{}}),
	}
	for _, l := range adds {
		if err := reg.Add(l); err != nil {
			t.Fatalf("Registry.Add(%s): %v", l.LocalName, err)
		}
	}

	return New(t.Context(), Config{Registry: reg, Telemetry: testTelemetry(t), Logger: testLogger()})
}

func TestNew_panicsOnMissingDependencies(t *testing.T) {
	t.Parallel()

	t.Run("nil registry", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("New: want panic for a nil Registry, got none")
			}
		}()
		New(t.Context(), Config{Telemetry: testTelemetry(t)})
	})

	t.Run("nil telemetry", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("New: want panic for a nil Telemetry, got none")
			}
		}()
		New(t.Context(), Config{Registry: pluginhost.NewRegistry()})
	})
}

func TestCatalog_satisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ providercatalog.Catalog = buildTestCatalog(t)
}

func TestCatalog_Model(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	tests := []struct {
		name    string
		ref     agentprofile.ModelRef
		wantErr bool
	}{
		{name: "registered primary", ref: agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"}},
		{name: "registered second provider", ref: agentprofile.ModelRef{Provider: "other-vendor", ID: "small-model"}},
		{name: "unknown provider", ref: agentprofile.ModelRef{Provider: "openai", ID: "claude-opus-4"}, wantErr: true},
		{name: "unknown id under a real provider", ref: agentprofile.ModelRef{Provider: "anthropic", ID: "claude-nonexistent"}, wantErr: true},
		{name: "a wrong-category provider present in the registry", ref: agentprofile.ModelRef{Provider: "fs", ID: "read_file"}, wantErr: true},
		{name: "a widget provider present in the registry", ref: agentprofile.ModelRef{Provider: "sidebar", ID: "x"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cat.Model(tt.ref)
			if tt.wantErr {
				if !errors.Is(err, providercatalog.ErrNotFound) {
					t.Fatalf("Model(%+v): want ErrNotFound, got %v", tt.ref, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Model(%+v): unexpected error: %v", tt.ref, err)
			}
			if got.Ref != tt.ref {
				t.Errorf("Model(%+v): Ref = %+v", tt.ref, got.Ref)
			}
			if got.Spec == nil || got.Spec.GetId() != tt.ref.ID {
				t.Errorf("Model(%+v): Spec = %+v", tt.ref, got.Spec)
			}
			if got.Producer.GetCategory() != commonv1.Category_CATEGORY_MODEL {
				t.Errorf("Model(%+v): Producer.Category = %v, want CATEGORY_MODEL", tt.ref, got.Producer.GetCategory())
			}
		})
	}
}

func TestCatalog_ModelSpecs(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	specs := cat.ModelSpecs()
	want := []agentprofile.ModelRef{
		{Provider: "anthropic", ID: "claude-opus-4"},
		{Provider: "anthropic", ID: "claude-haiku-4"},
		{Provider: "other-vendor", ID: "small-model"},
	}
	for _, ref := range want {
		if _, ok := specs[ref]; !ok {
			t.Errorf("ModelSpecs() missing %+v", ref)
		}
	}
	if len(specs) != len(want) {
		t.Errorf("ModelSpecs() = %d entries, want %d", len(specs), len(want))
	}

	// The returned map is a fresh copy — mutating it must not disturb a
	// later call.
	delete(specs, want[0])
	if _, ok := cat.ModelSpecs()[want[0]]; !ok {
		t.Error("ModelSpecs() a second time is missing an entry deleted from the first call's map — it must be a fresh copy")
	}
}

func TestCatalog_Tool(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	tests := []struct {
		name           string
		provider, tool string
		wantErr        bool
		wantPreview    bool
		wantTerminates bool
	}{
		{name: "fs.read_file supports preview", provider: "fs", tool: "read_file", wantPreview: true},
		{name: "shell.run: non-unimplemented error still means supported", provider: "shell", tool: "run", wantPreview: true, wantTerminates: true},
		{name: "unknown provider", provider: "ripgrep", tool: "search", wantErr: true},
		{name: "unknown tool under a real provider", provider: "fs", tool: "delete_file", wantErr: true},
		{name: "halves swapped", provider: "read_file", tool: "fs", wantErr: true},
		{name: "a model provider present in the registry", provider: "anthropic", tool: "read_file", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cat.Tool(tt.provider, tt.tool)
			if tt.wantErr {
				if !errors.Is(err, providercatalog.ErrNotFound) {
					t.Fatalf("Tool(%q, %q): want ErrNotFound, got %v", tt.provider, tt.tool, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Tool(%q, %q): unexpected error: %v", tt.provider, tt.tool, err)
			}
			if got.Provider != tt.provider {
				t.Errorf("Tool(%q, %q): Provider = %q", tt.provider, tt.tool, got.Provider)
			}
			if got.SupportsPreview != tt.wantPreview {
				t.Errorf("Tool(%q, %q): SupportsPreview = %v, want %v", tt.provider, tt.tool, got.SupportsPreview, tt.wantPreview)
			}
			if got.TerminatesTurn != tt.wantTerminates {
				t.Errorf("Tool(%q, %q): TerminatesTurn = %v, want %v", tt.provider, tt.tool, got.TerminatesTurn, tt.wantTerminates)
			}
		})
	}
}

// TestNew_malformedCapabilities confirms a provider whose Live.Capabilities
// is nil or of the wrong Go type for its own declared category — a
// producer misbehaving on its own GetCapabilities/GetSchema response —
// is logged and skipped rather than panicking New or corrupting another
// provider's extraction. Exercises the defensive branch every one of
// buildModels/buildTools/buildContexts falls back to.
func TestNew_malformedCapabilities(t *testing.T) {
	t.Parallel()

	reg := pluginhost.NewRegistry()
	adds := []*pluginhost.Live{
		live("bad-model", commonv1.Category_CATEGORY_MODEL, "bad-model-vendor", 0, nilModelClient(), nil),
		live("bad-tool", commonv1.Category_CATEGORY_TOOL, "bad-tool-vendor", 1, previewOK(),
			&modelv1.GetCapabilitiesResponse{}), // wrong type for CATEGORY_TOOL
		live("bad-context", commonv1.Category_CATEGORY_CONTEXT, "bad-context-vendor", 2, nilContextClient(),
			&contextv1.GetCapabilitiesResponse{}), // right type, nil nested Capabilities
		live("good-model", commonv1.Category_CATEGORY_MODEL, "good-vendor", 3, nilModelClient(),
			&modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{Models: []*modelv1.ModelSpec{{Id: "m"}}}}),
	}
	for _, l := range adds {
		if err := reg.Add(l); err != nil {
			t.Fatalf("Registry.Add(%s): %v", l.LocalName, err)
		}
	}

	cat := New(t.Context(), Config{Registry: reg, Telemetry: testTelemetry(t), Logger: testLogger()})

	if _, err := cat.Model(agentprofile.ModelRef{Provider: "bad-model", ID: "anything"}); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Errorf("Model(bad-model): want ErrNotFound, got %v", err)
	}
	if _, err := cat.Tool("bad-tool", "anything"); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Errorf("Tool(bad-tool): want ErrNotFound, got %v", err)
	}
	if contexts := cat.Contexts(); slices.ContainsFunc(contexts, func(h providercatalog.ContextHandle) bool { return h.Provider == "bad-context" }) {
		t.Errorf("Contexts() includes bad-context, want it skipped")
	}
	if _, err := cat.Model(agentprofile.ModelRef{Provider: "good-model", ID: "m"}); err != nil {
		t.Errorf("Model(good-model): unexpected error: %v — a malformed sibling must not corrupt extraction", err)
	}
}

// TestCatalog_Tool_unimplementedPreview isolates the Unimplemented case
// in its own registry (rather than folding it into buildTestCatalog)
// so its assertion reads unambiguously against a single, dedicated
// fixture.
func TestCatalog_Tool_unimplementedPreview(t *testing.T) {
	t.Parallel()

	reg := pluginhost.NewRegistry()
	if err := reg.Add(live("fs", commonv1.Category_CATEGORY_TOOL, "filesystem", 0, previewUnimplemented(),
		&toolv1.GetSchemaResponse{Tools: []*toolv1.ToolSchema{{Name: "write_file"}}})); err != nil {
		t.Fatalf("Registry.Add: %v", err)
	}
	cat := New(t.Context(), Config{Registry: reg, Telemetry: testTelemetry(t), Logger: testLogger()})

	got, err := cat.Tool("fs", "write_file")
	if err != nil {
		t.Fatalf("Tool: unexpected error: %v", err)
	}
	if got.SupportsPreview {
		t.Error("Tool(fs, write_file): SupportsPreview = true, want false (Preview answered Unimplemented)")
	}
}

func TestCatalog_ToolNames(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	got := cat.ToolNames()
	want := map[string][]string{
		"fs":    {"read_file"},
		"shell": {"run"},
	}
	for provider, names := range want {
		if !slices.Equal(got[provider], names) {
			t.Errorf("ToolNames()[%q] = %v, want %v", provider, got[provider], names)
		}
	}
	if _, ok := got["anthropic"]; ok {
		t.Error(`ToolNames() has a "anthropic" entry — a model provider must not appear`)
	}
}

func TestCatalog_Contexts(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	got := cat.Contexts()
	if len(got) != 2 {
		t.Fatalf("Contexts() = %d entries, want 2", len(got))
	}
	// claude-md (LaunchIndex 4) must sort before git-status (LaunchIndex
	// 5) even though git-status was registered first.
	if got[0].Provider != "claude-md" || got[1].Provider != "git-status" {
		t.Fatalf("Contexts() order = [%s, %s], want [claude-md, git-status]", got[0].Provider, got[1].Provider)
	}
	if got[0].Position != 4 || got[1].Position != 5 {
		t.Errorf("Contexts() Position = [%d, %d], want [4, 5]", got[0].Position, got[1].Position)
	}
	if got[0].TokenBudget != 4000 {
		t.Errorf("Contexts()[0].TokenBudget = %d, want 4000 (Capabilities.DefaultTokenBudget — see doc.go's known gap)", got[0].TokenBudget)
	}
	if !got[0].Capabilities.GetCompactor() {
		t.Error("Contexts()[0].Capabilities.Compactor = false, want true")
	}

	// The returned slice is a fresh copy — mutating it must not disturb
	// a later call's ordering.
	got[0], got[1] = got[1], got[0]
	if again := cat.Contexts(); again[0].Provider != "claude-md" {
		t.Errorf("Contexts() a second time = %q first, want claude-md — Contexts must return a copy", again[0].Provider)
	}
}

// TestCatalog_Hook confirms every provider in the fixture — including
// context/model/widget providers with unrelated capabilities — reports
// ErrNotFound. This is not evidence Hook() is unreachable in production:
// it is the unit-tier ceiling documented on live's doc comment. Only a
// real subprocess (pluginhost's own supervisor_integration_test.go)
// makes Live.HookClient() succeed, so the "found" branch of Hook()'s map
// lookup is exercised there, not here.
func TestCatalog_Hook(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	for _, provider := range []string{"anthropic", "fs", "shell", "claude-md", "sidebar", "absent"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			_, err := cat.Hook(provider)
			if !errors.Is(err, providercatalog.ErrNotFound) {
				t.Errorf("Hook(%q): want ErrNotFound, got %v", provider, err)
			}
		})
	}
}

// TestComposesWithAgentprofile mirrors drivers/fake's own
// TestComposesWithAgentprofile: proves this driver's ModelSpecs/ToolNames
// output really does compose with agentprofile.SelectModel/ResolveTools,
// not merely resemble their parameter shapes.
func TestComposesWithAgentprofile(t *testing.T) {
	t.Parallel()
	cat := buildTestCatalog(t)

	opus := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"}
	haiku := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-haiku-4"}
	block := agentprofile.ModelBlock{Primary: opus, Fallbacks: []agentprofile.ModelRef{haiku}}

	got, err := agentprofile.SelectModel(block, cat.ModelSpecs(), agentprofile.TurnRequirements{NeedsToolUse: true})
	if err != nil {
		t.Fatalf("SelectModel: unexpected error: %v", err)
	}
	if got != haiku {
		t.Errorf("SelectModel = %+v, want the tool-using fallback %+v", got, haiku)
	}

	resolved, err := agentprofile.ResolveTools([]string{"fs.*", "shell.run"}, cat.ToolNames())
	if err != nil {
		t.Fatalf("ResolveTools: unexpected error: %v", err)
	}
	want := []string{"fs.read_file", "shell.run"}
	gotNames := slices.Sorted(maps.Keys(resolved))
	if !slices.Equal(gotNames, want) {
		t.Fatalf("ResolveTools = %v, want %v", gotNames, want)
	}
}
