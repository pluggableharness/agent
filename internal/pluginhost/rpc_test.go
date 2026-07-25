package pluginhost

// Unit tier: the per-category RPC dispatch in rpc.go, exercised against
// real generated clients over an in-memory gRPC connection (a
// bufconn-backed pipe — no subprocess, no network, no filesystem). The
// dispatch is where a copy-paste slip is most likely and least visible:
// GetSchema for tool and GetCapabilities for the other six, and a
// ConfigSchema that sits nested inside a Capabilities message for five
// categories but flat on the response for tool and slashcommand.

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// producerFor builds the identity a fake category server reports.
func producerFor(category commonv1.Category) *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Name:     "fake-" + category.String(),
		Version:  "1.0.0",
		Source:   "github.com/agentco/fake",
		Category: category,
	}
}

// schemaFor builds a one-attribute ConfigSchema whose attribute is named
// after the category, so a test can tell which server answered.
func schemaFor(category commonv1.Category) *configv1.ConfigSchema {
	return &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
		{Name: category.String(), Type: configv1.AttrType_ATTR_TYPE_STRING},
	}}
}

// configured records the config a fake server's Configure received, so
// the dispatch's own argument passing is asserted rather than assumed.
type configured struct{ got *structpb.Struct }

type fakeModel struct {
	modelv1.UnimplementedModelServiceServer
	cfg *configured
}

func (s *fakeModel) Describe(context.Context, *modelv1.DescribeRequest) (*modelv1.DescribeResponse, error) {
	return &modelv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_MODEL)}, nil
}

func (s *fakeModel) GetCapabilities(context.Context, *modelv1.GetCapabilitiesRequest) (*modelv1.GetCapabilitiesResponse, error) {
	return &modelv1.GetCapabilitiesResponse{Capabilities: &modelv1.Capabilities{
		ConfigSchema: schemaFor(commonv1.Category_CATEGORY_MODEL),
	}}, nil
}

func (s *fakeModel) Configure(_ context.Context, req *modelv1.ConfigureRequest) (*modelv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &modelv1.ConfigureResponse{}, nil
}

type fakeTool struct {
	toolv1.UnimplementedToolServiceServer
	cfg *configured
}

func (s *fakeTool) Describe(context.Context, *toolv1.DescribeRequest) (*toolv1.DescribeResponse, error) {
	return &toolv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_TOOL)}, nil
}

func (s *fakeTool) GetSchema(context.Context, *toolv1.GetSchemaRequest) (*toolv1.GetSchemaResponse, error) {
	return &toolv1.GetSchemaResponse{ConfigSchema: schemaFor(commonv1.Category_CATEGORY_TOOL)}, nil
}

func (s *fakeTool) Configure(_ context.Context, req *toolv1.ConfigureRequest) (*toolv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &toolv1.ConfigureResponse{}, nil
}

type fakeContext struct {
	contextv1.UnimplementedContextServiceServer
	cfg *configured
}

func (s *fakeContext) Describe(context.Context, *contextv1.DescribeRequest) (*contextv1.DescribeResponse, error) {
	return &contextv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_CONTEXT)}, nil
}

func (s *fakeContext) GetCapabilities(context.Context, *contextv1.GetCapabilitiesRequest) (*contextv1.GetCapabilitiesResponse, error) {
	return &contextv1.GetCapabilitiesResponse{Capabilities: &contextv1.ContextCapabilities{
		ConfigSchema: schemaFor(commonv1.Category_CATEGORY_CONTEXT),
	}}, nil
}

func (s *fakeContext) Configure(_ context.Context, req *contextv1.ConfigureRequest) (*contextv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &contextv1.ConfigureResponse{}, nil
}

type fakeMemory struct {
	memoryv1.UnimplementedMemoryServiceServer
	cfg *configured
}

func (s *fakeMemory) Describe(context.Context, *memoryv1.DescribeRequest) (*memoryv1.DescribeResponse, error) {
	return &memoryv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_MEMORY)}, nil
}

func (s *fakeMemory) GetCapabilities(context.Context, *memoryv1.GetCapabilitiesRequest) (*memoryv1.GetCapabilitiesResponse, error) {
	return &memoryv1.GetCapabilitiesResponse{Capabilities: &memoryv1.MemoryCapabilities{
		ConfigSchema: schemaFor(commonv1.Category_CATEGORY_MEMORY),
	}}, nil
}

func (s *fakeMemory) Configure(_ context.Context, req *memoryv1.ConfigureRequest) (*memoryv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &memoryv1.ConfigureResponse{}, nil
}

type fakeFrontend struct {
	frontendv1.UnimplementedFrontendServiceServer
	cfg *configured
}

func (s *fakeFrontend) Describe(context.Context, *frontendv1.DescribeRequest) (*frontendv1.DescribeResponse, error) {
	return &frontendv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_FRONTEND)}, nil
}

func (s *fakeFrontend) GetCapabilities(context.Context, *frontendv1.GetCapabilitiesRequest) (*frontendv1.GetCapabilitiesResponse, error) {
	return &frontendv1.GetCapabilitiesResponse{Capabilities: &frontendv1.FrontendCapabilities{
		ConfigSchema: schemaFor(commonv1.Category_CATEGORY_FRONTEND),
	}}, nil
}

func (s *fakeFrontend) Configure(_ context.Context, req *frontendv1.ConfigureRequest) (*frontendv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &frontendv1.ConfigureResponse{}, nil
}

type fakeWidget struct {
	widgetv1.UnimplementedWidgetServiceServer
	cfg *configured
}

func (s *fakeWidget) Describe(context.Context, *widgetv1.DescribeRequest) (*widgetv1.DescribeResponse, error) {
	return &widgetv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_WIDGET)}, nil
}

func (s *fakeWidget) GetCapabilities(context.Context, *widgetv1.GetCapabilitiesRequest) (*widgetv1.GetCapabilitiesResponse, error) {
	return &widgetv1.GetCapabilitiesResponse{Capabilities: &widgetv1.WidgetCapabilities{
		ConfigSchema: schemaFor(commonv1.Category_CATEGORY_WIDGET),
	}}, nil
}

func (s *fakeWidget) Configure(_ context.Context, req *widgetv1.ConfigureRequest) (*widgetv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &widgetv1.ConfigureResponse{}, nil
}

type fakeSlashCommand struct {
	slashcommandv1.UnimplementedSlashCommandServiceServer
	cfg *configured
}

func (s *fakeSlashCommand) Describe(context.Context, *slashcommandv1.DescribeRequest) (*slashcommandv1.DescribeResponse, error) {
	return &slashcommandv1.DescribeResponse{Producer: producerFor(commonv1.Category_CATEGORY_SLASHCOMMAND)}, nil
}

func (s *fakeSlashCommand) GetCapabilities(context.Context, *slashcommandv1.GetCapabilitiesRequest) (*slashcommandv1.GetCapabilitiesResponse, error) {
	// Deliberately flat, not nested in a Capabilities message — the
	// asymmetry fetchCapabilities has to know about.
	return &slashcommandv1.GetCapabilitiesResponse{ConfigSchema: schemaFor(commonv1.Category_CATEGORY_SLASHCOMMAND)}, nil
}

func (s *fakeSlashCommand) Configure(_ context.Context, req *slashcommandv1.ConfigureRequest) (*slashcommandv1.ConfigureResponse, error) {
	s.cfg.got = req.GetConfig()
	return &slashcommandv1.ConfigureResponse{}, nil
}

// dial starts an in-memory gRPC server with register applied and returns
// a connection to it, torn down at test cleanup.
func dial(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1 << 16)
	server := grpc.NewServer()
	register(server)
	go func() {
		// A closed listener at cleanup is the normal end of this
		// goroutine, not a failure worth reporting.
		_ = server.Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = lis.Close()
	})
	return conn
}

func TestRPCDispatch_everyCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category commonv1.Category
		serve    func(*grpc.Server, *configured)
		client   func(*grpc.ClientConn) any
		// wantCapsType asserts fetchCapabilities returned the whole
		// category-specific response, not just the schema.
		wantCapsType func(any) bool
	}{
		{
			name:     "model",
			category: commonv1.Category_CATEGORY_MODEL,
			serve: func(s *grpc.Server, c *configured) {
				modelv1.RegisterModelServiceServer(s, &fakeModel{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return modelv1.NewModelServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*modelv1.GetCapabilitiesResponse); return ok },
		},
		{
			name:     "tool",
			category: commonv1.Category_CATEGORY_TOOL,
			serve: func(s *grpc.Server, c *configured) {
				toolv1.RegisterToolServiceServer(s, &fakeTool{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return toolv1.NewToolServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*toolv1.GetSchemaResponse); return ok },
		},
		{
			name:     "context",
			category: commonv1.Category_CATEGORY_CONTEXT,
			serve: func(s *grpc.Server, c *configured) {
				contextv1.RegisterContextServiceServer(s, &fakeContext{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return contextv1.NewContextServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*contextv1.GetCapabilitiesResponse); return ok },
		},
		{
			name:     "memory",
			category: commonv1.Category_CATEGORY_MEMORY,
			serve: func(s *grpc.Server, c *configured) {
				memoryv1.RegisterMemoryServiceServer(s, &fakeMemory{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return memoryv1.NewMemoryServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*memoryv1.GetCapabilitiesResponse); return ok },
		},
		{
			name:     "frontend",
			category: commonv1.Category_CATEGORY_FRONTEND,
			serve: func(s *grpc.Server, c *configured) {
				frontendv1.RegisterFrontendServiceServer(s, &fakeFrontend{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return frontendv1.NewFrontendServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*frontendv1.GetCapabilitiesResponse); return ok },
		},
		{
			name:     "widget",
			category: commonv1.Category_CATEGORY_WIDGET,
			serve: func(s *grpc.Server, c *configured) {
				widgetv1.RegisterWidgetServiceServer(s, &fakeWidget{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return widgetv1.NewWidgetServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*widgetv1.GetCapabilitiesResponse); return ok },
		},
		{
			name:     "slashcommand",
			category: commonv1.Category_CATEGORY_SLASHCOMMAND,
			serve: func(s *grpc.Server, c *configured) {
				slashcommandv1.RegisterSlashCommandServiceServer(s, &fakeSlashCommand{cfg: c})
			},
			client:       func(conn *grpc.ClientConn) any { return slashcommandv1.NewSlashCommandServiceClient(conn) },
			wantCapsType: func(v any) bool { _, ok := v.(*slashcommandv1.GetCapabilitiesResponse); return ok },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorded := &configured{}
			conn := dial(t, func(s *grpc.Server) { tt.serve(s, recorded) })
			client := tt.client(conn)
			ctx := context.Background()

			producer, err := describeProducer(ctx, client)
			if err != nil {
				t.Fatalf("describeProducer: %v", err)
			}
			if producer.GetCategory() != tt.category {
				t.Errorf("describeProducer category = %v, want %v", producer.GetCategory(), tt.category)
			}
			if want := "fake-" + tt.category.String(); producer.GetName() != want {
				t.Errorf("describeProducer name = %q, want %q", producer.GetName(), want)
			}

			capabilities, schema, err := fetchCapabilities(ctx, client)
			if err != nil {
				t.Fatalf("fetchCapabilities: %v", err)
			}
			if !tt.wantCapsType(capabilities) {
				t.Errorf("fetchCapabilities returned %T, want this category's own response message", capabilities)
			}
			if len(schema.GetAttributes()) != 1 {
				t.Fatalf("fetchCapabilities schema = %v, want the one declared attribute", schema)
			}
			if got := schema.GetAttributes()[0].GetName(); got != tt.category.String() {
				t.Errorf("fetchCapabilities schema attribute = %q, want %q — the ConfigSchema was read from the wrong place", got, tt.category.String())
			}

			cfg := mustStruct(t, map[string]any{"k": "v"})
			if err := configurePlugin(ctx, client, cfg); err != nil {
				t.Fatalf("configurePlugin: %v", err)
			}
			if recorded.got.GetFields()["k"].GetStringValue() != "v" {
				t.Errorf("Configure received %v, want the passed config", recorded.got)
			}
		})
	}
}

// TestRPCDispatch_unknownClient confirms an unrecognized client type
// fails loudly on all three entry points rather than silently doing
// nothing — the failure mode if internal/pluginruntime ever grows a
// category this file has not been taught about.
func TestRPCDispatch_unknownClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	notAClient := struct{}{}

	if _, err := describeProducer(ctx, notAClient); !errors.Is(err, ErrUnknownClient) {
		t.Errorf("describeProducer = %v, want ErrUnknownClient", err)
	}
	if _, _, err := fetchCapabilities(ctx, notAClient); !errors.Is(err, ErrUnknownClient) {
		t.Errorf("fetchCapabilities = %v, want ErrUnknownClient", err)
	}
	if err := configurePlugin(ctx, notAClient, nil); !errors.Is(err, ErrUnknownClient) {
		t.Errorf("configurePlugin = %v, want ErrUnknownClient", err)
	}
}

// TestRPCDispatch_errorsAreWrapped confirms an RPC failure keeps its
// gRPC status while gaining this package's own prefix — a plugin that
// does not implement Describe must surface as an error, not a nil
// producer treated as success.
func TestRPCDispatch_errorsAreWrapped(t *testing.T) {
	t.Parallel()

	// A server serving only ToolService, dialed with a model client:
	// every model RPC answers Unimplemented. This is exactly the shape
	// the dev-override category probe relies on.
	conn := dial(t, func(s *grpc.Server) {
		toolv1.RegisterToolServiceServer(s, &fakeTool{cfg: &configured{}})
	})
	client := modelv1.NewModelServiceClient(conn)
	ctx := context.Background()

	if _, err := describeProducer(ctx, client); err == nil {
		t.Error("describeProducer against the wrong category = nil error, want Unimplemented")
	}
	if _, _, err := fetchCapabilities(ctx, client); err == nil {
		t.Error("fetchCapabilities against the wrong category = nil error, want Unimplemented")
	}
	if err := configurePlugin(ctx, client, nil); err == nil {
		t.Error("configurePlugin against the wrong category = nil error, want Unimplemented")
	}
}

func TestWrapRPC(t *testing.T) {
	t.Parallel()

	if err := wrapRPC("describe", nil); err != nil {
		t.Errorf("wrapRPC with a nil error = %v, want nil", err)
	}
	sentinel := errors.New("boom")
	err := wrapRPC("describe", sentinel)
	if !errors.Is(err, sentinel) {
		t.Errorf("wrapRPC lost the wrapped error: %v", err)
	}
	if got := err.Error(); got != "pluginhost: describe: boom" {
		t.Errorf("wrapRPC = %q, want the package- and operation-prefixed form", got)
	}
}

// TestProbeCategories_coversEverySeven guards the dev-override probe
// against silently skipping a category: a new commonv1.Category with no
// entry here would make a plugin of that category unprobeable.
func TestProbeCategories_coversEverySeven(t *testing.T) {
	t.Parallel()

	seen := make(map[commonv1.Category]bool, len(probeCategories))
	for _, c := range probeCategories {
		if seen[c] {
			t.Errorf("probeCategories lists %v twice", c)
		}
		seen[c] = true
	}
	for value := range commonv1.Category_name {
		category := commonv1.Category(value)
		if category == commonv1.Category_CATEGORY_UNSPECIFIED {
			continue
		}
		if !seen[category] {
			t.Errorf("probeCategories omits %v; a dev-override plugin of that category could never be probed", category)
		}
	}
}
