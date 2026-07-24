package frontend

import (
	"errors"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
)

// Attach implements FrontendServiceServer's one bidirectional RPC — the
// single, connection-scoped, multiplexed event channel a frontend keeps
// open for its connection's whole lifetime (doc.go's "Attach is one stream
// per connection, not one per session"). This is a single dispatch loop,
// never one goroutine per session's own stream: every ClientEvent this
// connection receives, regardless of which session it names, is read from
// the same stream.Recv() call in arrival order and handed to
// svc.provider.HandleEvent, which answers through a connection-scoped
// Emitter shared by every session on this connection — so a session's
// backfill batch (SessionAttached, replayed Renders, BackfillComplete),
// like every other event, is written only to the one *connection this
// Attach call owns, never fanned out to any other connection
// (frontend-protocol.md's "Backfill is unicast to the attaching stream
// only, never broadcast").
//
// See doc.go's "Wire direction" section for why this method RECEIVES
// ClientEvent and SENDS ServerEvent, the mechanical direction the
// generated frontendv1.FrontendServiceServer interface fixes.
func (svc *Service) Attach(stream frontendv1.FrontendService_AttachServer) error {
	ctx := stream.Context()
	conn := &connection{stream: stream}

	for {
		in, err := stream.Recv()
		if err != nil {
			return terminal(err)
		}

		event, convErr := fromClientEventProto(in)
		if convErr != nil {
			if sendErr := conn.emitError(nil, &Error{
				Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT,
				Message:  convErr.Error(),
			}); sendErr != nil {
				return sendErr
			}
			continue
		}

		if err := svc.provider.HandleEvent(ctx, event, conn); err != nil {
			var fatal *FatalErr
			if errors.As(err, &fatal) {
				return fatal.Err
			}
			if sendErr := conn.emitError(requestIDOf(event), inBandError(err)); sendErr != nil {
				return sendErr
			}
		}
	}
}

// terminal maps a stream.Recv error to what Attach itself should return:
// nil for ordinary stream closure (io.EOF, signaling the kernel called
// CloseSend) or expected cancellation (codes.Canceled — normal control
// flow per .claude/rules/grpc.md, never logged as a failure), or the error
// itself otherwise — a genuinely fatal transport condition that legitimately
// closes the stream with a gRPC status.
func terminal(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	if status.Code(err) == codes.Canceled {
		return nil
	}
	return err
}

// connection adapts one Attach stream to the Emitter interface. mu guards
// Send, which grpc.ServerStream does not support calling concurrently from
// more than one goroutine — a real concern here, since a Provider may
// retain the Emitter past its own HandleEvent call to push an unsolicited
// ServerEvent from another goroutine while the dispatch loop above is
// concurrently emitting an in-band error for a later ClientEvent.
type connection struct {
	stream frontendv1.FrontendService_AttachServer
	mu     sync.Mutex
}

var _ Emitter = (*connection)(nil)

// Emit sends event to the kernel over this connection's Attach stream.
func (c *connection) Emit(event ServerEvent) error {
	out, convErr := toServerEventProto(event)
	if convErr != nil {
		return convErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream.Send(out)
}

// emitError sends fe in-band as an ErrorEvent, correlated to requestID
// when non-nil — the mid-Attach error path (doc.go's "Error handling is
// two distinct paths, not one"). Its own Send failure is returned
// unwrapped so Attach's dispatch loop treats it exactly like any other
// broken-stream condition: fatal, closing the RPC with a gRPC status.
func (c *connection) emitError(requestID *string, fe *Error) error {
	return c.Emit(ServerEvent{RequestID: requestID, Payload: ErrorEvent{Err: fe}})
}

// requestIDOf returns the request_id to correlate an in-band error back to
// the ClientEvent control message that triggered it
// (frontend-protocol.md's ServerEvent.request_id note), or nil for a
// session-scoped variant, which carries no request_id of its own.
func requestIDOf(event ClientEvent) *string {
	switch p := event.Payload.(type) {
	case CreateSession:
		return strPtr(p.RequestID)
	case AttachSession:
		return strPtr(p.RequestID)
	case ResumeSession:
		return strPtr(p.RequestID)
	case DetachSession:
		return strPtr(p.RequestID)
	case ListSessions:
		return strPtr(p.RequestID)
	default:
		return nil
	}
}

func strPtr(s string) *string { return &s }
