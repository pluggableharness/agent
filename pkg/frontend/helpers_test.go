package frontend_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/frontend"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// newTestServer starts svc on an in-memory bufconn listener and returns a
// frontendv1.FrontendServiceClient dialed against it — a real gRPC round
// trip, not a hand-rolled interface fake, mirroring
// pkg/kernel/helpers_test.go's newTestClient.
func newTestServer(t *testing.T, svc *frontend.Service) frontendv1.FrontendServiceClient {
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

	return frontendv1.NewFrontendServiceClient(conn)
}

// fakeProvider is a hand-written frontend.Provider fake (go-testing.md:
// fakes, not mocking frameworks). Each method's behavior is controlled by
// a caller-set func field; a nil field falls through to a harmless zero
// value.
type fakeProvider struct {
	capabilitiesFunc func(ctx context.Context) (*frontend.Capabilities, error)
	configureFunc    func(ctx context.Context, config *structpb.Struct) error
	handleEventFunc  func(ctx context.Context, event frontend.ClientEvent, emit frontend.Emitter) error
}

var _ frontend.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Capabilities(ctx context.Context) (*frontend.Capabilities, error) {
	if f.capabilitiesFunc != nil {
		return f.capabilitiesFunc(ctx)
	}
	return &frontend.Capabilities{}, nil
}

func (f *fakeProvider) Configure(ctx context.Context, config *structpb.Struct) error {
	if f.configureFunc != nil {
		return f.configureFunc(ctx, config)
	}
	return nil
}

func (f *fakeProvider) HandleEvent(ctx context.Context, event frontend.ClientEvent, emit frontend.Emitter) error {
	if f.handleEventFunc != nil {
		return f.handleEventFunc(ctx, event, emit)
	}
	return nil
}

// testIdentity is a fixed plugin.Identity used across server/attach tests.
var testIdentity = plugin.Identity{Name: "test-frontend", Version: "1.0.0", Source: "github.com/pluggableharness/agent/pkg/frontend"}
