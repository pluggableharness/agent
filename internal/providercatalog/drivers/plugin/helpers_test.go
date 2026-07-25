package plugin

import (
	"context"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"

	"github.com/pluggableharness/agent/internal/pluginhost"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
)

// live builds a *pluginhost.Live for this package's tests, populating
// only the exported fields settable from outside internal/pluginhost.
// Its unexported plugin/closeFn fields stay nil, so live.HookClient()
// always reports ok=false — the same constraint pluginhost's own
// registry_test.go documents ("HookClient() on a Live that never came
// from a launch reported ok = true") and the reason this package's own
// Hook() tests can only exercise the ErrNotFound paths at the unit tier;
// the "found" happy path is pluginhost's own
// supervisor_integration_test.go's job, since a real subprocess is the
// only thing that ever makes HookClient() succeed. See extract_test.go's
// TestSupportedHookPoints for how this package still exercises its own
// per-category extraction logic directly, decoupled from that
// constraint.
func live(localName string, category commonv1.Category, producerName string, launchIndex int, client, capabilities any) *pluginhost.Live {
	return &pluginhost.Live{
		LocalName:    localName,
		Producer:     &commonv1.ProducerRef{Category: category, Name: producerName, Version: "1.0.0"},
		Client:       client,
		Capabilities: capabilities,
		LaunchIndex:  launchIndex,
	}
}

func nilModelClient() modelv1.ModelServiceClient       { return modelv1.NewModelServiceClient(nil) }
func nilContextClient() contextv1.ContextServiceClient { return contextv1.NewContextServiceClient(nil) }

// fakeToolClient is a hand-written toolv1.ToolServiceClient fake
// (.claude/rules/go-testing.md: fakes, not mocking frameworks), mirroring
// internal/tooldispatch's fakeToolClient. Only Preview is meaningful
// here — everything else panics via the embedded nil client, since this
// package never calls them.
type fakeToolClient struct {
	toolv1.ToolServiceClient

	previewFunc func(ctx context.Context, req *toolv1.PreviewRequest) (*toolv1.PreviewResponse, error)
}

func (f *fakeToolClient) Preview(ctx context.Context, req *toolv1.PreviewRequest, _ ...grpc.CallOption) (*toolv1.PreviewResponse, error) {
	return f.previewFunc(ctx, req)
}

// previewOK returns a fakeToolClient whose Preview succeeds.
func previewOK() *fakeToolClient {
	return &fakeToolClient{previewFunc: func(context.Context, *toolv1.PreviewRequest) (*toolv1.PreviewResponse, error) {
		return &toolv1.PreviewResponse{}, nil
	}}
}

// previewUnimplemented returns a fakeToolClient whose Preview reports
// codes.Unimplemented, as pkg/tool/server.go's own Preview handler does
// for a Provider that does not additionally implement Previewer.
func previewUnimplemented() *fakeToolClient {
	return &fakeToolClient{previewFunc: func(context.Context, *toolv1.PreviewRequest) (*toolv1.PreviewResponse, error) {
		return nil, status.Error(codes.Unimplemented, "tool: preview not implemented by this provider")
	}}
}

// previewInvalidArgument returns a fakeToolClient whose Preview reports
// an error distinct from Unimplemented — a Previewer that was reached
// but rejected this call's synthetic probe arguments. This is the case
// doc.go's "Resolving SupportsPreview" calls out by name: the answer
// must still be "supported."
func previewInvalidArgument() *fakeToolClient {
	return &fakeToolClient{previewFunc: func(context.Context, *toolv1.PreviewRequest) (*toolv1.PreviewResponse, error) {
		return nil, status.Error(codes.InvalidArgument, "tool: preview: bad arguments")
	}}
}

// previewBlocks returns a fakeToolClient whose Preview blocks until ctx
// is done, for exercising resolveSupportsPreview's timeout handling.
// status.FromContextError converts the raw context error into the same
// shape a real grpc client transport hands back (codes.Canceled /
// codes.DeadlineExceeded) — a hand-rolled fake returning a bare
// context.Canceled/context.DeadlineExceeded would make status.Code
// report codes.Unknown instead, since that mapping is transport
// machinery this in-process fake otherwise bypasses entirely.
func previewBlocks() *fakeToolClient {
	return &fakeToolClient{previewFunc: func(ctx context.Context, _ *toolv1.PreviewRequest) (*toolv1.PreviewResponse, error) {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}}
}

// testLogger returns a discarding *slog.Logger — this package's tests
// assert on Catalog's returned handles, not on log content.
func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testTelemetry returns a real *telemetry.Provider backed by a fresh
// fake.Backend, matching internal/contextassembly/helpers_test.go's own
// testTelemetry/testAssembler pattern.
func testTelemetry(t *testing.T) *telemetry.Provider {
	t.Helper()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "providercatalog-plugin-test"
	backend := fake.New()
	prov, err := telemetry.New(t.Context(), cfg, backend, nil)
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

// Fixtures below build minimal, realistic per-category capability
// responses for extract_test.go's TestSupportedHookPoints, which
// exercises all seven plugin categories directly — decoupled from
// buildHooks' HookClient() gate, which no unit test can satisfy (see
// live's doc comment above).

func memoryCapabilities(points ...commonv1.HookPoint) *memoryv1.GetCapabilitiesResponse {
	return &memoryv1.GetCapabilitiesResponse{Capabilities: &memoryv1.MemoryCapabilities{SupportedHookPoints: points}}
}

func frontendCapabilities(points ...commonv1.HookPoint) *frontendv1.GetCapabilitiesResponse {
	return &frontendv1.GetCapabilitiesResponse{Capabilities: &frontendv1.FrontendCapabilities{SupportedHookPoints: points}}
}

func widgetCapabilities(points ...commonv1.HookPoint) *widgetv1.GetCapabilitiesResponse {
	return &widgetv1.GetCapabilitiesResponse{Capabilities: &widgetv1.WidgetCapabilities{SupportedHookPoints: points}}
}

func slashcommandCapabilities(points ...commonv1.HookPoint) *slashcommandv1.GetCapabilitiesResponse {
	return &slashcommandv1.GetCapabilitiesResponse{SupportedHookPoints: points}
}
