package context

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

// fakeKernelServer is a hand-written kernelv1.KernelCallbackServiceServer
// fake (go-testing.md: fakes, not mocking frameworks), covering only the
// one RPC this package's SDK calls: CountTokens.
type fakeKernelServer struct {
	kernelv1.UnimplementedKernelCallbackServiceServer

	countTokensFunc func(*kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error)
}

func (f *fakeKernelServer) CountTokens(ctx context.Context, req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
	if f.countTokensFunc != nil {
		return f.countTokensFunc(req)
	}
	return f.UnimplementedKernelCallbackServiceServer.CountTokens(ctx, req)
}

// newTestKernelClient starts srv on an in-memory bufconn listener and
// returns a *kernel.Client dialed against it — a real gRPC round trip, so
// tests exercising countTokens/CountTokens prove they actually call
// through the kernel callback channel rather than estimating locally.
// Mirrors pkg/kernel/helpers_test.go's newTestClient (unexported there,
// so not importable directly from this package's tests).
func newTestKernelClient(t *testing.T, srv kernelv1.KernelCallbackServiceServer) *kernel.Client {
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
