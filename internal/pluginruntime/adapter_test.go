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
	providerv1 "github.com/pluggableharness/agent/pkg/provider/proto/v1"
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

func TestPluginMap(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	cb := &fakeCallbackServer{}

	for _, category := range []commonv1.Category{
		commonv1.Category_CATEGORY_PROVIDER,
		commonv1.Category_CATEGORY_TOOL,
		commonv1.Category_CATEGORY_CONTEXT,
		commonv1.Category_CATEGORY_MEMORY,
		commonv1.Category_CATEGORY_FRONTEND,
		commonv1.Category_CATEGORY_WIDGET,
	} {
		t.Run(category.String(), func(t *testing.T) {
			t.Parallel()

			set := pluginMap(category, cb, prov)
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
		{commonv1.Category_CATEGORY_PROVIDER, providerv1.ProviderServiceClient(nil)},
		{commonv1.Category_CATEGORY_TOOL, toolv1.ToolServiceClient(nil)},
		{commonv1.Category_CATEGORY_CONTEXT, contextv1.ContextServiceClient(nil)},
		{commonv1.Category_CATEGORY_MEMORY, memoryv1.MemoryServiceClient(nil)},
		{commonv1.Category_CATEGORY_FRONTEND, frontendv1.FrontendServiceClient(nil)},
		{commonv1.Category_CATEGORY_WIDGET, widgetv1.WidgetServiceClient(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.category.String(), func(t *testing.T) {
			t.Parallel()

			got, err := newCategoryClient(tt.category, nil)
			if err != nil {
				t.Fatalf("newCategoryClient: %v", err)
			}
			switch tt.category {
			case commonv1.Category_CATEGORY_PROVIDER:
				if _, ok := got.(providerv1.ProviderServiceClient); !ok {
					t.Fatalf("got %T, want providerv1.ProviderServiceClient", got)
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

// TestCategoryPlugin_brokerOnce exercises the "serve the callback broker
// exactly once" guarantee directly against categoryPlugin.brokerOnce,
// standing in for GRPCClient's own use of it. A real *plugin.GRPCBroker
// has no exported constructor (confirmed: newGRPCBroker is unexported and
// needs an unexported streamer type this package cannot supply), so the
// "AcceptAndServe called once" assertion against a genuine broker lives in
// the integration tier (launch_integration_test.go); this test covers the
// sync.Once semantics categoryPlugin actually relies on.
func TestCategoryPlugin_brokerOnce(t *testing.T) {
	t.Parallel()

	p := &categoryPlugin{}
	var calls int
	var mu sync.Mutex

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			p.brokerOnce.Do(func() {
				mu.Lock()
				calls++
				mu.Unlock()
			})
		})
	}
	wg.Wait()

	if calls != 1 {
		t.Fatalf("brokerOnce fired %d times, want 1", calls)
	}
}

func TestCategoryPlugin_newCallbackServer(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	cb := &fakeCallbackServer{}
	p := &categoryPlugin{category: commonv1.Category_CATEGORY_TOOL, callback: cb, telemetry: prov}

	server := p.newCallbackServer(nil)
	if server == nil {
		t.Fatal("newCallbackServer returned nil")
	}
	if _, ok := server.GetServiceInfo()["pluggableharness.agent.kernel.v1.KernelCallbackService"]; !ok {
		t.Fatalf("KernelCallbackService not registered: %v", server.GetServiceInfo())
	}
}
