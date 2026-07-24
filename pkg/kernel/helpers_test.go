package kernel_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/pluggableharness/agent/pkg/kernel"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// newTestClient starts srv on an in-memory bufconn listener and returns a
// *kernel.Client dialed against it — a real gRPC round trip, not a hand-
// rolled interface fake, so these tests exercise the actual wire
// marshaling this package's translation code produces.
func newTestClient(t *testing.T, srv kernelv1.KernelCallbackServiceServer) *kernel.Client {
	t.Helper()

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

	gs := grpc.NewServer()
	kernelv1.RegisterKernelCallbackServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return kernel.NewClient(conn)
}

// fakeServer is a hand-written kernelv1.KernelCallbackServiceServer fake
// (go-testing.md: fakes, not mocking frameworks). Each RPC's behavior is
// controlled by a caller-set func field; a nil field falls through to the
// embedded UnimplementedKernelCallbackServiceServer's codes.Unimplemented.
type fakeServer struct {
	kernelv1.UnimplementedKernelCallbackServiceServer

	logFunc                func(*kernelv1.LogRequest) (*kernelv1.LogResult, error)
	exportSpansFunc        func(*kernelv1.ExportSpansRequest) (*kernelv1.ExportSpansResult, error)
	getTelemetryConfigFunc func(*kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error)
	getConfigFunc          func(*kernelv1.GetConfigRequest) (*kernelv1.GetConfigResult, error)
	publishFunc            func(*kernelv1.PublishRequest) (*kernelv1.PublishResult, error)
	subscribeFunc          func(*kernelv1.SubscribeRequest, kernelv1.KernelCallbackService_SubscribeServer) error
	runSessionFunc         func(*kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error)
	countTokensFunc        func(*kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error)
	emitFunc               func(*kernelv1.EmitRequest) (*kernelv1.EmitResult, error)
	getSessionFunc         func(*kernelv1.GetSessionRequest) (*kernelv1.GetSessionResult, error)
	readEventsFunc         func(*kernelv1.ReadEventsRequest, kernelv1.KernelCallbackService_ReadEventsServer) error
}

func (f *fakeServer) Log(ctx context.Context, req *kernelv1.LogRequest) (*kernelv1.LogResult, error) {
	if f.logFunc != nil {
		return f.logFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.Log(ctx, req)
}

func (f *fakeServer) ExportSpans(ctx context.Context, req *kernelv1.ExportSpansRequest) (*kernelv1.ExportSpansResult, error) {
	if f.exportSpansFunc != nil {
		return f.exportSpansFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.ExportSpans(ctx, req)
}

func (f *fakeServer) GetTelemetryConfig(ctx context.Context, req *kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error) {
	if f.getTelemetryConfigFunc != nil {
		return f.getTelemetryConfigFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.GetTelemetryConfig(ctx, req)
}

func (f *fakeServer) GetConfig(ctx context.Context, req *kernelv1.GetConfigRequest) (*kernelv1.GetConfigResult, error) {
	if f.getConfigFunc != nil {
		return f.getConfigFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.GetConfig(ctx, req)
}

func (f *fakeServer) Publish(ctx context.Context, req *kernelv1.PublishRequest) (*kernelv1.PublishResult, error) {
	if f.publishFunc != nil {
		return f.publishFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.Publish(ctx, req)
}

func (f *fakeServer) Subscribe(req *kernelv1.SubscribeRequest, stream kernelv1.KernelCallbackService_SubscribeServer) error {
	if f.subscribeFunc != nil {
		return f.subscribeFunc(req, stream)
	}
	return f.UnimplementedKernelCallbackServiceServer.Subscribe(req, stream)
}

func (f *fakeServer) RunSession(ctx context.Context, req *kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error) {
	if f.runSessionFunc != nil {
		return f.runSessionFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.RunSession(ctx, req)
}

func (f *fakeServer) CountTokens(ctx context.Context, req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
	if f.countTokensFunc != nil {
		return f.countTokensFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.CountTokens(ctx, req)
}

func (f *fakeServer) Emit(ctx context.Context, req *kernelv1.EmitRequest) (*kernelv1.EmitResult, error) {
	if f.emitFunc != nil {
		return f.emitFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.Emit(ctx, req)
}

func (f *fakeServer) GetSession(ctx context.Context, req *kernelv1.GetSessionRequest) (*kernelv1.GetSessionResult, error) {
	if f.getSessionFunc != nil {
		return f.getSessionFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.GetSession(ctx, req)
}

func (f *fakeServer) ReadEvents(req *kernelv1.ReadEventsRequest, stream kernelv1.KernelCallbackService_ReadEventsServer) error {
	if f.readEventsFunc != nil {
		return f.readEventsFunc(req, stream)
	}
	return f.UnimplementedKernelCallbackServiceServer.ReadEvents(req, stream)
}

var _ kernelv1.KernelCallbackServiceServer = (*fakeServer)(nil)
