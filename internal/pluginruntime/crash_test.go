package pluginruntime

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/common"
)

func TestCrashError(t *testing.T) {
	t.Parallel()

	underlying := errors.New("connection reset")
	err := &CrashError{Err: underlying}

	if !errors.Is(err, underlying) {
		t.Errorf("errors.Is(err, underlying) = false, want true")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("status.FromError: not a gRPC status error")
	}
	if st.Code() != codes.Unavailable {
		t.Errorf("status code = %v, want %v", st.Code(), codes.Unavailable)
	}
	if err.Error() == "" {
		t.Error("Error() is empty")
	}
}

func TestClassifyErr(t *testing.T) {
	t.Parallel()

	rpcErr := status.Error(codes.Internal, "boom")

	tests := []struct {
		name      string
		err       error
		exited    func() bool
		wantCrash bool
		wantExact bool // if true, want the exact same err back (passthrough)
	}{
		{
			name:      "nil error",
			err:       nil,
			exited:    func() bool { return true },
			wantExact: true,
		},
		{
			name:      "context.Canceled always passes through, even if exited",
			err:       context.Canceled,
			exited:    func() bool { return true },
			wantExact: true,
		},
		{
			name:      "codes.Canceled always passes through, even if exited",
			err:       status.Error(codes.Canceled, "canceled"),
			exited:    func() bool { return true },
			wantExact: true,
		},
		{
			name:      "not exited: error passes through unwrapped",
			err:       rpcErr,
			exited:    func() bool { return false },
			wantExact: true,
		},
		{
			name:      "nil exited func: never classified as a crash",
			err:       rpcErr,
			exited:    nil,
			wantExact: true,
		},
		{
			name:      "exited: error wrapped as CrashError",
			err:       rpcErr,
			exited:    func() bool { return true },
			wantCrash: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifyErr(tt.err, tt.exited)
			if tt.wantExact {
				if !errors.Is(got, tt.err) {
					t.Fatalf("classifyErr = %v, want passthrough of %v", got, tt.err)
				}
				if _, ok := errors.AsType[*CrashError](got); ok {
					t.Fatalf("classifyErr wrapped a CrashError, want passthrough: %v", got)
				}
				return
			}
			if tt.wantCrash {
				if _, ok := errors.AsType[*CrashError](got); !ok {
					t.Fatalf("classifyErr = %v, want a *CrashError", got)
				}
				if !errors.Is(got, tt.err) {
					t.Errorf("errors.Is(got, tt.err) = false, want true (Unwrap chain)")
				}
			}
		})
	}
}

func TestClientHolder_exited(t *testing.T) {
	t.Parallel()

	h := &clientHolder{}
	// Before the holder is populated, exited must conservatively report
	// false — an error observed before Launch stores the real client must
	// never be misclassified as a crash.
	if h.exited() {
		t.Error("exited() = true before the holder is populated, want false")
	}
}

func TestClientHolder_exited_populated(t *testing.T) {
	t.Parallel()

	// A real *plugin.Client that was never started (plugin.NewClient only
	// builds a struct) delegates cleanly to its own Exited(), which
	// reports false until the subprocess has actually run — this
	// exercises the holder's non-nil path, distinct from the
	// not-yet-populated case above.
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  common.Handshake,
		Plugins:          plugin.PluginSet{},
		Cmd:              exec.CommandContext(t.Context(), "true"),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
	})
	h := &clientHolder{}
	h.client.Store(client)

	if h.exited() {
		t.Error("exited() = true for a never-started client, want false")
	}
}

func TestCrashUnaryInterceptor(t *testing.T) {
	t.Parallel()

	rpcErr := status.Error(codes.Internal, "boom")
	interceptor := crashUnaryInterceptor(func() bool { return true })

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return rpcErr
	}
	err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)

	var ce *CrashError
	if !errors.As(err, &ce) {
		t.Fatalf("interceptor error = %v, want *CrashError", err)
	}
}

func TestCrashUnaryInterceptor_success(t *testing.T) {
	t.Parallel()

	interceptor := crashUnaryInterceptor(func() bool { return true })
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return nil
	}
	if err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatalf("interceptor error = %v, want nil", err)
	}
}

// fakeClientStream is a hand-written grpc.ClientStream fake exercising
// only RecvMsg/SendMsg, per go-testing.md.
type fakeClientStream struct {
	grpc.ClientStream
	recvErr error
	sendErr error
}

func (s *fakeClientStream) RecvMsg(any) error { return s.recvErr }
func (s *fakeClientStream) SendMsg(any) error { return s.sendErr }

func TestCrashStreamInterceptor_establishFailure(t *testing.T) {
	t.Parallel()

	rpcErr := status.Error(codes.Internal, "boom")
	interceptor := crashStreamInterceptor(func() bool { return true })
	streamer := func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
		return nil, rpcErr
	}

	_, err := interceptor(context.Background(), &grpc.StreamDesc{}, nil, "/svc/Method", streamer)
	var ce *CrashError
	if !errors.As(err, &ce) {
		t.Fatalf("interceptor error = %v, want *CrashError", err)
	}
}

func TestCrashStreamInterceptor_wrapsRecvAndSend(t *testing.T) {
	t.Parallel()

	rpcErr := status.Error(codes.Internal, "boom")
	fake := &fakeClientStream{recvErr: rpcErr, sendErr: rpcErr}
	interceptor := crashStreamInterceptor(func() bool { return true })
	streamer := func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
		return fake, nil
	}

	cs, err := interceptor(context.Background(), &grpc.StreamDesc{}, nil, "/svc/Method", streamer)
	if err != nil {
		t.Fatalf("interceptor: unexpected error: %v", err)
	}

	var ce *CrashError
	if !errors.As(cs.RecvMsg(nil), &ce) {
		t.Errorf("RecvMsg error not a *CrashError")
	}
	ce = nil
	if !errors.As(cs.SendMsg(nil), &ce) {
		t.Errorf("SendMsg error not a *CrashError")
	}

	// A clean io.EOF (end of stream, not a crash) must pass through
	// unwrapped even when exited() is true — classifyErr only crash-wraps
	// once the underlying call actually failed with a non-nil error other
	// than the ones it special-cases; io.EOF is a real error value here,
	// so this exercises that classifyErr wraps it like any other
	// non-Canceled error when exited.
	fake.recvErr = io.EOF
	got := cs.RecvMsg(nil)
	ce = nil
	if !errors.As(got, &ce) {
		t.Errorf("RecvMsg(io.EOF) with exited()=true = %v, want *CrashError", got)
	}
}
