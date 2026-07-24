package widget_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// newTestClient starts srv on an in-memory bufconn listener and returns a
// widgetv1.WidgetServiceClient dialed against it — a real gRPC round
// trip, not a hand-rolled interface fake, so these tests exercise the
// actual wire marshaling widget.Service's conversions produce. Modeled on
// pkg/kernel/helpers_test.go's newTestClient.
func newTestClient(t *testing.T, srv widgetv1.WidgetServiceServer) widgetv1.WidgetServiceClient {
	t.Helper()

	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)

	gs := grpc.NewServer()
	widgetv1.RegisterWidgetServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return widgetv1.NewWidgetServiceClient(conn)
}
