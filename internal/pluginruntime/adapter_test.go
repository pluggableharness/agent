package pluginruntime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hashicorp/go-plugin"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// newTestTelemetry returns an in-memory, no-network telemetry.Provider
// (the fake driver — no real OTel export), for tests that just need a
// real *telemetry.Provider value to construct dependent types with.
func newTestTelemetry(t *testing.T) *telemetry.Provider {
	t.Helper()
	prov, err := telemetry.New(context.Background(), telemetry.DefaultConfig, fake.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return prov
}

// allCategories is every commonv1.Category a plugin map can be keyed by —
// the full set the dev_overrides category probe keys one subprocess with.
var allCategories = []commonv1.Category{
	commonv1.Category_CATEGORY_MODEL,
	commonv1.Category_CATEGORY_TOOL,
	commonv1.Category_CATEGORY_CONTEXT,
	commonv1.Category_CATEGORY_MEMORY,
	commonv1.Category_CATEGORY_FRONTEND,
	commonv1.Category_CATEGORY_WIDGET,
	commonv1.Category_CATEGORY_SLASHCOMMAND,
}

func TestPluginMap(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	cb := &fakeCallbackServer{}

	for _, category := range allCategories {
		t.Run(category.String(), func(t *testing.T) {
			t.Parallel()

			set := pluginMap(newLaunchScope(cb, prov), category)
			if len(set) != 1 {
				t.Fatalf("pluginMap: %d entries, want 1", len(set))
			}
			key := common.PluginKey(category)
			p, ok := set[key]
			if !ok {
				t.Fatalf("pluginMap: no entry for key %q", key)
			}
			if _, ok := p.(plugin.GRPCPlugin); !ok {
				t.Fatalf("pluginMap[%q] does not implement plugin.GRPCPlugin", key)
			}
		})
	}
}

// TestPluginMap_multiCategorySharesOneScope covers the dev_overrides
// category-probe shape: one subprocess keyed by every category at once,
// because the binary's real category isn't known ahead of time. Every
// entry must point at the *same* launchScope — that shared pointer is
// what makes the callback broker's exactly-once guarantee hold across all
// seven (see TestLaunchScope_serveOnce).
func TestPluginMap_multiCategorySharesOneScope(t *testing.T) {
	t.Parallel()

	scope := newLaunchScope(&fakeCallbackServer{}, newTestTelemetry(t))
	set := pluginMap(scope, allCategories...)

	if len(set) != len(allCategories) {
		t.Fatalf("pluginMap: %d entries, want %d", len(set), len(allCategories))
	}
	for _, category := range allCategories {
		key := common.PluginKey(category)
		p, ok := set[key]
		if !ok {
			t.Fatalf("pluginMap: no entry for key %q", key)
		}
		cp, ok := p.(*categoryPlugin)
		if !ok {
			t.Fatalf("pluginMap[%q] = %T, want *categoryPlugin", key, p)
		}
		if cp.category != category {
			t.Errorf("pluginMap[%q].category = %v, want %v", key, cp.category, category)
		}
		if cp.scope != scope {
			t.Errorf("pluginMap[%q].scope = %p, want the single shared scope %p", key, cp.scope, scope)
		}
	}
}

func TestCategoryPlugin_GRPCServer_alwaysFails(t *testing.T) {
	t.Parallel()

	p := &categoryPlugin{category: commonv1.Category_CATEGORY_TOOL}
	err := p.GRPCServer(nil, nil)
	if !errors.Is(err, errGRPCServerUnsupported) {
		t.Fatalf("GRPCServer: error = %v, want errors.Is errGRPCServerUnsupported", err)
	}
}

func TestNewCategoryClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category commonv1.Category
		want     any
	}{
		{commonv1.Category_CATEGORY_MODEL, modelv1.ModelServiceClient(nil)},
		{commonv1.Category_CATEGORY_TOOL, toolv1.ToolServiceClient(nil)},
		{commonv1.Category_CATEGORY_CONTEXT, contextv1.ContextServiceClient(nil)},
		{commonv1.Category_CATEGORY_MEMORY, memoryv1.MemoryServiceClient(nil)},
		{commonv1.Category_CATEGORY_FRONTEND, frontendv1.FrontendServiceClient(nil)},
		{commonv1.Category_CATEGORY_WIDGET, widgetv1.WidgetServiceClient(nil)},
		{commonv1.Category_CATEGORY_SLASHCOMMAND, slashcommandv1.SlashCommandServiceClient(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.category.String(), func(t *testing.T) {
			t.Parallel()

			got, err := newCategoryClient(tt.category, nil)
			if err != nil {
				t.Fatalf("newCategoryClient: %v", err)
			}
			switch tt.category {
			case commonv1.Category_CATEGORY_MODEL:
				if _, ok := got.(modelv1.ModelServiceClient); !ok {
					t.Fatalf("got %T, want modelv1.ModelServiceClient", got)
				}
			case commonv1.Category_CATEGORY_TOOL:
				if _, ok := got.(toolv1.ToolServiceClient); !ok {
					t.Fatalf("got %T, want toolv1.ToolServiceClient", got)
				}
			case commonv1.Category_CATEGORY_CONTEXT:
				if _, ok := got.(contextv1.ContextServiceClient); !ok {
					t.Fatalf("got %T, want contextv1.ContextServiceClient", got)
				}
			case commonv1.Category_CATEGORY_MEMORY:
				if _, ok := got.(memoryv1.MemoryServiceClient); !ok {
					t.Fatalf("got %T, want memoryv1.MemoryServiceClient", got)
				}
			case commonv1.Category_CATEGORY_FRONTEND:
				if _, ok := got.(frontendv1.FrontendServiceClient); !ok {
					t.Fatalf("got %T, want frontendv1.FrontendServiceClient", got)
				}
			case commonv1.Category_CATEGORY_WIDGET:
				if _, ok := got.(widgetv1.WidgetServiceClient); !ok {
					t.Fatalf("got %T, want widgetv1.WidgetServiceClient", got)
				}
			case commonv1.Category_CATEGORY_SLASHCOMMAND:
				if _, ok := got.(slashcommandv1.SlashCommandServiceClient); !ok {
					t.Fatalf("got %T, want slashcommandv1.SlashCommandServiceClient", got)
				}
			}
		})
	}
}

func TestNewCategoryClient_unrecognized(t *testing.T) {
	t.Parallel()

	_, err := newCategoryClient(commonv1.Category_CATEGORY_UNSPECIFIED, nil)
	if !errors.Is(err, errUnrecognizedCategory) {
		t.Fatalf("newCategoryClient: error = %v, want errors.Is errUnrecognizedCategory", err)
	}
}

// TestLaunchScope_serveOnce exercises the "serve the callback broker
// exactly once per launched subprocess" guarantee directly against
// launchScope.doServeOnce — the once-guarded core
// launchScope.serveCallbackOnce wraps, and the sole reason the fixed
// pkg/common.CallbackBrokerID is collision-free (CLAUDE.md). A real
// *plugin.GRPCBroker has no exported constructor (confirmed: newGRPCBroker
// is unexported and needs an unexported streamer type this package cannot
// supply), so the "AcceptAndServe called once" assertion against a genuine
// broker lives in the integration tier (launch_integration_test.go).
//
// The multi-category subtest is the safety-critical one: it drives
// doServeOnce concurrently through every categoryPlugin of a seven-entry
// plugin map — the dev_overrides category-probe shape — and proves a
// single AcceptAndServe still results, which a per-categoryPlugin
// sync.Once would not have guaranteed.
func TestLaunchScope_serveOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		categories []commonv1.Category
		// racers is how many goroutines pile onto each categoryPlugin.
		racers int
	}{
		{"single category", []commonv1.Category{commonv1.Category_CATEGORY_TOOL}, 5},
		{"every category on one subprocess", allCategories, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scope := newLaunchScope(&fakeCallbackServer{}, newTestTelemetry(t))
			set := pluginMap(scope, tt.categories...)

			var mu sync.Mutex
			var calls int

			var wg sync.WaitGroup
			for _, p := range set {
				cp, ok := p.(*categoryPlugin)
				if !ok {
					t.Fatalf("plugin map entry = %T, want *categoryPlugin", p)
				}
				for range tt.racers {
					wg.Go(func() {
						cp.scope.doServeOnce(func() {
							mu.Lock()
							calls++
							mu.Unlock()
						})
					})
				}
			}
			wg.Wait()

			if calls != 1 {
				t.Fatalf("callback broker served %d times across %d categories, want exactly 1 — the fixed CallbackBrokerID is only collision-free at exactly one AcceptAndServe per subprocess", calls, len(tt.categories))
			}
		})
	}
}

func TestLaunchScope_newCallbackServer(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	cb := &fakeCallbackServer{}
	scope := newLaunchScope(cb, prov)

	server := scope.newCallbackServer(nil)
	if server == nil {
		t.Fatal("newCallbackServer returned nil")
	}
	if _, ok := server.GetServiceInfo()["pluggableharness.kernel.v1.KernelCallbackService"]; !ok {
		t.Fatalf("KernelCallbackService not registered: %v", server.GetServiceInfo())
	}
}

func TestLaunchScope_clientConn(t *testing.T) {
	t.Parallel()

	scope := newLaunchScope(&fakeCallbackServer{}, newTestTelemetry(t))
	if got := scope.clientConn(); got != nil {
		t.Fatalf("clientConn() = %v before any dispense, want nil", got)
	}
}
