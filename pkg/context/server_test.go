package context_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	pluggablecontext "github.com/pluggableharness/agent/pkg/context"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	"github.com/pluggableharness/agent/pkg/render"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// newTestContextClient starts svc on an in-memory bufconn listener and
// returns a contextv1.ContextServiceClient dialed against it — a real
// gRPC round trip over the actual generated ContextService, not a direct
// Go method call, so these tests exercise wire marshaling and gRPC status
// mapping too.
func newTestContextClient(t *testing.T, svc *pluggablecontext.Service) contextv1.ContextServiceClient {
	t.Helper()

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

	return contextv1.NewContextServiceClient(conn)
}

func testIdentity() plugin.Identity {
	return plugin.Identity{Name: "claude-md", Version: "1.0.0", Source: "github.com/agentco/context-claude-md"}
}

func TestService_GetCapabilities(t *testing.T) {
	t.Parallel()

	schema := &configv1.ConfigSchema{}
	provider := &fakeProvider{
		getCapabilitiesFunc: func() (*pluggablecontext.Capabilities, error) {
			return pluggablecontext.NewCapabilities(2000, pluggablecontext.StabilityStatic, schema, pluggablecontext.WithCompactor()), nil
		},
	}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	resp, err := client.GetCapabilities(t.Context(), &contextv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities() error = %v, want nil", err)
	}
	caps := resp.GetCapabilities()
	if caps.GetDefaultTokenBudget() != 2000 {
		t.Errorf("DefaultTokenBudget = %d, want 2000", caps.GetDefaultTokenBudget())
	}
	if !caps.GetCompactor() {
		t.Error("Compactor = false, want true")
	}
}

func TestService_GetCapabilities_error(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		getCapabilitiesFunc: func() (*pluggablecontext.Capabilities, error) {
			return nil, &pluggablecontext.Error{Category: pluggablecontext.ErrorCategoryUnknown, Message: "boom"}
		},
	}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	_, err := client.GetCapabilities(t.Context(), &contextv1.GetCapabilitiesRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) ok = false, want true", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Code() = %v, want %v", st.Code(), codes.Internal)
	}
}

func TestService_Configure(t *testing.T) {
	t.Parallel()

	var gotConfig *structpb.Struct
	provider := &fakeProvider{
		configureFunc: func(cfg *structpb.Struct) error {
			gotConfig = cfg
			return nil
		},
	}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	cfg, err := structpb.NewStruct(map[string]any{"path": "CLAUDE.md"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if _, err := client.Configure(t.Context(), &contextv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure() error = %v, want nil", err)
	}
	if gotConfig.GetFields()["path"].GetStringValue() != "CLAUDE.md" {
		t.Errorf("Configure() delivered config = %v, want path=CLAUDE.md", gotConfig)
	}
}

func TestService_Configure_error(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		configureFunc: func(*structpb.Struct) error {
			return &pluggablecontext.Error{Category: pluggablecontext.ErrorCategorySourceUnavailable, Message: "glob resolves to nothing"}
		},
	}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	_, err := client.Configure(t.Context(), &contextv1.ConfigureRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) ok = false, want true", err)
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("Code() = %v, want %v", st.Code(), codes.Unavailable)
	}
}

func TestService_Contribute_invalidRequest(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	req := &contextv1.ContextRequest{
		PriorSections: []*contentv1.ContextSection{
			{Provider: "other", Content: []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Image{Image: &contentv1.ImageBlock{}}}}},
		},
	}
	_, err := client.Contribute(t.Context(), req)
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) ok = false, want true", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("Code() = %v, want %v", st.Code(), codes.InvalidArgument)
	}
}

func TestService_Contribute_callbackDialFailure(t *testing.T) {
	t.Parallel()

	// plugin.NewCallback() with no broker set (never launched via
	// plugin.Serve) always fails to dial — see
	// pkg/plugin/callback_internal_test.go's identical documented
	// limitation. This proves Service.Contribute surfaces that failure
	// as a real gRPC error rather than panicking or hanging.
	provider := &fakeProvider{}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	_, err := client.Contribute(t.Context(), &contextv1.ContextRequest{})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) ok = false, want true", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("Code() = %v, want %v", st.Code(), codes.Internal)
	}
}

func TestService_Render_unimplemented(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	_, err := client.Render(t.Context(), &contextv1.RenderRequest{SchemaVersion: "v1"})
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError(%v) ok = false, want true", err)
	}
	if st.Code() != codes.Unimplemented {
		t.Errorf("Code() = %v, want %v", st.Code(), codes.Unimplemented)
	}
}

// rendererProvider implements both Provider and Renderer.
type rendererProvider struct {
	fakeProvider

	renderFunc func(*pluggablecontext.RenderRequest) (*renderv1.RenderTree, error)
}

func (r *rendererProvider) Render(_ context.Context, req *pluggablecontext.RenderRequest) (*renderv1.RenderTree, error) {
	return r.renderFunc(req)
}

var _ pluggablecontext.Renderer = (*rendererProvider)(nil)

func TestService_Render_implemented(t *testing.T) {
	t.Parallel()

	var gotSchemaVersion string
	provider := &rendererProvider{
		renderFunc: func(req *pluggablecontext.RenderRequest) (*renderv1.RenderTree, error) {
			gotSchemaVersion = req.SchemaVersion
			return render.Tree(render.Text("collapsed CLAUDE.md")), nil
		},
	}
	client := newTestContextClient(t, pluggablecontext.NewService(provider, testIdentity(), plugin.NewCallback()))

	resp, err := client.Render(t.Context(), &contextv1.RenderRequest{SchemaVersion: "v1", Payload: []byte("payload")})
	if err != nil {
		t.Fatalf("Render() error = %v, want nil", err)
	}
	if gotSchemaVersion != "v1" {
		t.Errorf("Renderer received SchemaVersion = %q, want %q", gotSchemaVersion, "v1")
	}
	if resp.GetTree().GetRoot() == nil {
		t.Error("Render() response tree root = nil, want a node")
	}
}

func TestService_Describe(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{}
	identity := testIdentity()
	client := newTestContextClient(t, pluggablecontext.NewService(provider, identity, plugin.NewCallback()))

	resp, err := client.Describe(t.Context(), &contextv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe() error = %v, want nil", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != identity.Name {
		t.Errorf("Producer.Name = %q, want %q", producer.GetName(), identity.Name)
	}
	if producer.GetVersion() != identity.Version {
		t.Errorf("Producer.Version = %q, want %q", producer.GetVersion(), identity.Version)
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_CONTEXT {
		t.Errorf("Producer.Category = %v, want CATEGORY_CONTEXT", producer.GetCategory())
	}
}
