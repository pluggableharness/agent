package tool_test

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
	"github.com/pluggableharness/agent/pkg/tool"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// fakeTool is a hand-written tool.Tool fake (go-testing.md: fakes, not
// mocking frameworks). Schema is served from the schema/schemaErr fields;
// Invoke's behavior is controlled by a caller-set func field, and a nil one
// falls through to sending a bare terminal result.
type fakeTool struct {
	schema     *tool.Schema
	schemaErr  error
	invokeFunc func(ctx context.Context, call *tool.Call, stream *tool.Stream) error
}

func (f *fakeTool) Schema() (*tool.Schema, error) {
	if f.schemaErr != nil {
		return nil, f.schemaErr
	}
	return f.schema, nil
}

func (f *fakeTool) Invoke(ctx context.Context, call *tool.Call, stream *tool.Stream) error {
	if f.invokeFunc != nil {
		return f.invokeFunc(ctx, call, stream)
	}
	return stream.Send(tool.NewResultEvent(map[string]any{}))
}

var _ tool.Tool = (*fakeTool)(nil)

// newFakeTool returns a *fakeTool exposing a minimally valid schema under
// name and the default Invoke — enough for any test that just needs a tool
// to exist under a given name.
func newFakeTool(name string) *fakeTool {
	return &fakeTool{schema: validSchema(name)}
}

// invokingTool returns a *fakeTool named name whose Invoke is fn.
func invokingTool(name string, fn func(ctx context.Context, call *tool.Call, stream *tool.Stream) error) *fakeTool {
	return &fakeTool{schema: validSchema(name), invokeFunc: fn}
}

// fakePreviewTool embeds *fakeTool and additionally implements the optional
// per-tool tool.Previewer. Kept a distinct type from fakeTool so tests can
// also exercise the "this tool does not preview" fallback against a plain
// *fakeTool — including both within one provider, which is the shape
// protocol.md#preview's per-operation MAY actually permits.
type fakePreviewTool struct {
	*fakeTool

	previewFunc func(ctx context.Context, call *tool.Call) (*renderv1.RenderTree, error)
}

func (f *fakePreviewTool) Preview(ctx context.Context, call *tool.Call) (*renderv1.RenderTree, error) {
	return f.previewFunc(ctx, call)
}

var (
	_ tool.Tool      = (*fakePreviewTool)(nil)
	_ tool.Previewer = (*fakePreviewTool)(nil)
)

// previewingTool returns a *fakePreviewTool named name whose Preview is fn.
func previewingTool(name string, fn func(ctx context.Context, call *tool.Call) (*renderv1.RenderTree, error)) *fakePreviewTool {
	return &fakePreviewTool{fakeTool: newFakeTool(name), previewFunc: fn}
}

// fakeProvider is a hand-written tool.Provider fake serving a fixed set of
// Tools. Configure's behavior is controlled by a caller-set func field; a
// nil field falls through to a harmless default.
type fakeProvider struct {
	tools         []tool.Tool
	configureFunc func(ctx context.Context, config map[string]any) error
}

func (f *fakeProvider) Tools() []tool.Tool {
	return f.tools
}

func (f *fakeProvider) Configure(ctx context.Context, config map[string]any) error {
	if f.configureFunc != nil {
		return f.configureFunc(ctx, config)
	}
	return nil
}

var _ tool.Provider = (*fakeProvider)(nil)

// newFakeProvider returns a *fakeProvider exposing tools.
func newFakeProvider(tools ...tool.Tool) *fakeProvider {
	return &fakeProvider{tools: tools}
}

// fakeFullProvider embeds fakeProvider and additionally implements every
// optional plugin-level interface this package defines (Renderer,
// ConfigSchemaProvider, SlashCommandProvider, HookPointProvider), each
// controlled by its own func field — used by tests exercising the
// optional-capability paths. Kept as a distinct type from fakeProvider so
// tests can also exercise the "provider does not implement this optional
// interface" fallback paths against a plain *fakeProvider. Previewer is
// absent by design: it is per-Tool, so it lives on fakePreviewTool.
type fakeFullProvider struct {
	*fakeProvider

	renderFunc       func(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
	configSchemaFunc func() (*configv1.ConfigSchema, error)
	slashCommands    []*commonv1.PromptExpansionSpec
	hookPoints       []commonv1.HookPoint
}

func (f *fakeFullProvider) Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error) {
	return f.renderFunc(ctx, payload, schemaVersion)
}

// ConfigSchema tolerates an unset configSchemaFunc because NewService calls
// it during construction — every test building a *fakeFullProvider would
// otherwise have to set a field it does not care about.
func (f *fakeFullProvider) ConfigSchema() (*configv1.ConfigSchema, error) {
	if f.configSchemaFunc == nil {
		return &configv1.ConfigSchema{}, nil
	}
	return f.configSchemaFunc()
}

func (f *fakeFullProvider) SlashCommands() []*commonv1.PromptExpansionSpec {
	return f.slashCommands
}

func (f *fakeFullProvider) SupportedHookPoints() []commonv1.HookPoint {
	return f.hookPoints
}

var (
	_ tool.Provider             = (*fakeFullProvider)(nil)
	_ tool.Renderer             = (*fakeFullProvider)(nil)
	_ tool.ConfigSchemaProvider = (*fakeFullProvider)(nil)
	_ tool.SlashCommandProvider = (*fakeFullProvider)(nil)
	_ tool.HookPointProvider    = (*fakeFullProvider)(nil)
)

// validSchema returns a minimally valid *tool.Schema for tests
// that just need something toProtoSchema accepts.
func validSchema(name string) *tool.Schema {
	return &tool.Schema{
		Name:         name,
		Kind:         tool.KindDataSource,
		Risk:         tool.RiskClassReadOnly,
		Description:  "a test operation",
		InputSchema:  &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
		OutputSchema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
		Streaming:    false,
		Concurrency:  &tool.ConcurrencySpec{Safe: true},
	}
}

// newTestClient starts a *tool.Service wrapping p on an in-memory bufconn
// listener and returns a real toolv1.ToolServiceClient dialed against it —
// a real gRPC round trip (go-testing.md), not a hand-rolled interface
// fake, mirroring pkg/kernel/helpers_test.go's newTestClient.
func newTestClient(t *testing.T, p tool.Provider) toolv1.ToolServiceClient {
	t.Helper()

	svc, err := tool.NewService(p, plugin.Identity{Name: "fake-tool", Version: "0.0.1", Source: "local/fake"}, plugin.NewCallback())
	if err != nil {
		t.Fatalf("tool.NewService: %v", err)
	}

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

	return toolv1.NewToolServiceClient(conn)
}
