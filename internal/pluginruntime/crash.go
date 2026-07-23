package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CrashError wraps an RPC failure that occurred after the underlying
// plugin subprocess had already exited — the category-agnostic crash
// signal plugin-runtime.md's "process_crashed" contract describes.
// Category-specific callers (a tool or provider driver) layer their own
// ToolErrorCategory/etc. on top via errors.As(err, &CrashError{}); this
// package has no notion of per-category error enums.
type CrashError struct {
	// Err is the underlying gRPC failure observed after the crash.
	Err error
}

// Error implements the error interface.
func (e *CrashError) Error() string {
	return fmt.Sprintf("pluginruntime: plugin process exited: %v", e.Err)
}

// Unwrap supports errors.Is/errors.As against the wrapped RPC error.
func (e *CrashError) Unwrap() error {
	return e.Err
}

// GRPCStatus reports codes.Unavailable, per grpc.md's canonical mapping
// for a crashed plugin process, so status.Code(err) and status.FromError
// both see the right code even through the errors.As indirection.
func (e *CrashError) GRPCStatus() *status.Status {
	return status.New(codes.Unavailable, e.Error())
}

// clientHolder exists to break the chicken-and-egg dependency between
// plugin.NewClient's required GRPCDialOptions (which must reference the
// crash interceptors below) and the *plugin.Client those interceptors
// need to call Exited() on, which does not exist until plugin.NewClient
// returns. Launch constructs a clientHolder, builds the dial options
// (interceptors close over holder.exited, not over any *plugin.Client
// directly), calls plugin.NewClient, and only then stores the resulting
// client into holder — all before the client is ever dialed, so no RPC
// can observe an unset holder.
type clientHolder struct {
	client atomic.Pointer[plugin.Client]
}

// exited reports whether the held *plugin.Client's subprocess has exited.
// Before the holder is populated (should never happen once Launch has
// called plugin.NewClient) it conservatively reports false, so an error
// observed before that point is never misclassified as a crash.
func (h *clientHolder) exited() bool {
	c := h.client.Load()
	if c == nil {
		return false
	}
	return c.Exited()
}

// classifyErr passes context.Canceled/codes.Canceled through untouched —
// normal control flow, per grpc.md, never wrapped or logged as a failure
// — and otherwise wraps err in a *CrashError when exited reports the
// plugin subprocess has already exited.
func classifyErr(err error, exited func() bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return err
	}
	if exited != nil && exited() {
		return &CrashError{Err: err}
	}
	return err
}

// crashUnaryInterceptor returns a grpc.UnaryClientInterceptor that
// classifies a failed unary RPC via classifyErr, using exited to detect a
// dead subprocess.
func crashUnaryInterceptor(exited func() bool) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		return classifyErr(err, exited)
	}
}

// crashStreamInterceptor returns a grpc.StreamClientInterceptor that
// classifies a failed stream-establishment call via classifyErr, and
// wraps the resulting stream so subsequent Send/RecvMsg failures are
// classified the same way.
func crashStreamInterceptor(exited func() bool) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		cs, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			return nil, classifyErr(err, exited)
		}
		return &crashClassifyingStream{ClientStream: cs, exited: exited}, nil
	}
}

// crashClassifyingStream wraps a grpc.ClientStream so that every
// Send/RecvMsg failure is classified the same way a unary RPC failure is.
type crashClassifyingStream struct {
	grpc.ClientStream
	exited func() bool
}

// RecvMsg classifies a failed receive via classifyErr.
func (s *crashClassifyingStream) RecvMsg(m any) error {
	return classifyErr(s.ClientStream.RecvMsg(m), s.exited)
}

// SendMsg classifies a failed send via classifyErr.
func (s *crashClassifyingStream) SendMsg(m any) error {
	return classifyErr(s.ClientStream.SendMsg(m), s.exited)
}
