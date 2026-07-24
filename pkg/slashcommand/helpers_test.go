package slashcommand_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	"github.com/pluggableharness/agent/pkg/slashcommand"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
)

// fakeProvider is a hand-written slashcommand.Provider fake (go-testing.md:
// fakes, not mocking frameworks). Each method's behavior is controlled by a
// caller-set func field; a nil field falls through to a harmless default.
type fakeProvider struct {
	capabilitiesFunc func(ctx context.Context) ([]*slashcommand.Spec, error)
	configureFunc    func(ctx context.Context, config map[string]any) error
	invokeFunc       func(ctx context.Context, call *slashcommand.Call, stream *slashcommand.Stream) error
}

func (f *fakeProvider) Capabilities(ctx context.Context) ([]*slashcommand.Spec, error) {
	if f.capabilitiesFunc != nil {
		return f.capabilitiesFunc(ctx)
	}
	return nil, nil
}

func (f *fakeProvider) Configure(ctx context.Context, config map[string]any) error {
	if f.configureFunc != nil {
		return f.configureFunc(ctx, config)
	}
	return nil
}

func (f *fakeProvider) Invoke(ctx context.Context, call *slashcommand.Call, stream *slashcommand.Stream) error {
	if f.invokeFunc != nil {
		return f.invokeFunc(ctx, call, stream)
	}
	return stream.Send(slashcommand.NewResultEvent(map[string]any{}))
}

var _ slashcommand.Provider = (*fakeProvider)(nil)

// fakeFullProvider embeds fakeProvider and additionally implements every
// optional interface this package defines (Renderer, Previewer,
// ConfigSchemaProvider, HookPointProvider), each controlled by its own func
// field — used by tests exercising the optional-capability paths. Kept as a
// distinct type from fakeProvider so tests can also exercise the "provider
// does not implement this optional interface" fallback paths against a
// plain *fakeProvider.
type fakeFullProvider struct {
	*fakeProvider

	renderFunc       func(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
	previewFunc      func(ctx context.Context, call *slashcommand.Call) (*renderv1.RenderTree, error)
	configSchemaFunc func() (*configv1.ConfigSchema, error)
	hookPoints       []commonv1.HookPoint
}

func (f *fakeFullProvider) Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error) {
	return f.renderFunc(ctx, payload, schemaVersion)
}

func (f *fakeFullProvider) Preview(ctx context.Context, call *slashcommand.Call) (*renderv1.RenderTree, error) {
	return f.previewFunc(ctx, call)
}

func (f *fakeFullProvider) ConfigSchema() (*configv1.ConfigSchema, error) {
	return f.configSchemaFunc()
}

func (f *fakeFullProvider) SupportedHookPoints() []commonv1.HookPoint {
	return f.hookPoints
}

var (
	_ slashcommand.Provider             = (*fakeFullProvider)(nil)
	_ slashcommand.Renderer             = (*fakeFullProvider)(nil)
	_ slashcommand.Previewer            = (*fakeFullProvider)(nil)
	_ slashcommand.ConfigSchemaProvider = (*fakeFullProvider)(nil)
	_ slashcommand.HookPointProvider    = (*fakeFullProvider)(nil)
)

// validSpec returns a minimally valid *slashcommand.Spec for tests that
// just need something toProtoSpec (via GetCapabilities) accepts.
func validSpec(name string) *slashcommand.Spec {
	return &slashcommand.Spec{
		Name:        name,
		Description: "a test command",
		InputSchema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
		Kind:        tool.KindDataSource,
		Risk:        tool.RiskClassReadOnly,
		Streaming:   false,
		Concurrency: &tool.ConcurrencySpec{Safe: true},
	}
}

// newTestClient starts a *slashcommand.Service wrapping p on an in-memory
// bufconn listener and returns a real slashcommandv1.SlashCommandServiceClient
// dialed against it — a real gRPC round trip (go-testing.md), not a
// hand-rolled interface fake, mirroring pkg/tool/helpers_test.go's
// newTestClient.
func newTestClient(t *testing.T, p slashcommand.Provider) slashcommandv1.SlashCommandServiceClient {
	t.Helper()

	svc := slashcommand.NewService(p, plugin.Identity{Name: "fake-slashcommand", Version: "0.0.1", Source: "local/fake"}, plugin.NewCallback())

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

	gs := grpc.NewServer()
	svc.Register(gs)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return slashcommandv1.NewSlashCommandServiceClient(conn)
}
