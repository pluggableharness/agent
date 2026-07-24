package tool

import (
	"context"
	"errors"
	"sync"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// Sentinel errors returned by Stream.Send.
var (
	// ErrStreamClosed is returned by Send once a terminal Result or
	// Error event has already been sent — the stream contract
	// (docs/specifications/tool/protocol.md#invoke: "exactly one of
	// result or error MUST close the stream") forbids sending anything
	// after that.
	ErrStreamClosed = errors.New("tool: invoke stream already closed by a terminal result or error event")
	// ErrResultAfterCancel is returned by Send when asked to send a
	// success Result event after the stream's context has already been
	// cancelled. docs/specifications/tool/protocol.md#invoke: "A plugin
	// MUST NOT synthesize a result claiming full success after a
	// cancelled operation." — send a partial_result/output_chunk best-
	// effort report, or an error event with ErrorCategoryCancelled,
	// instead.
	ErrResultAfterCancel = errors.New("tool: cannot send a success result after the stream's context was canceled")
	// ErrDuplicateExitStatus is returned by Send on a second exit_status
	// event within one stream. docs/specifications/tool/data-types.md#toolcall--toolevent--toolresult:
	// "exit_status MAY appear at most once."
	ErrDuplicateExitStatus = errors.New("tool: exit_status may appear at most once per invoke stream")
)

// Stream is the cancellation-safe sender a Provider's Invoke uses to emit
// ToolEvents, per docs/specifications/tool/protocol.md#invoke. It enforces
// the parts of the Invoke stream contract that are mechanically checkable
// from the sequence of Send calls alone:
//
//   - exactly one of a terminal Result or Error event closes the stream;
//     any Send after that returns ErrStreamClosed.
//   - exit_status appears at most once; a second one returns
//     ErrDuplicateExitStatus.
//   - a success Result is refused once the stream's own context has been
//     cancelled, so a Provider cannot synthesize a false "succeeded"
//     terminal event after cancellation (ErrResultAfterCancel) — send a
//     partial_result or an error event with ErrorCategoryCancelled
//     instead.
//   - output_chunk (and every other event) ordering is preserved because
//     Send serializes every call under one mutex rather than writing to
//     the underlying gRPC stream directly from more than one goroutine.
//
// What Stream deliberately does NOT enforce: whether exit_status belongs
// on this particular call at all. docs/specifications/tool/protocol.md#invoke
// restricts exit_status to process-backed (exec-family) operations, but
// that fact lives in the *operation's* documentation/convention, not on
// the wire Call or Schema — there is no process_backed field to
// check against. Enforcing "at most once" is this package's chosen,
// mechanically-checkable substitute; whether a given operation should ever
// call NewExitStatusEvent at all remains a Provider-author discipline
// concern, exactly as the spec frames it ("a provider for a non-process-
// backed tool ... MUST NOT emit this").
type Stream struct {
	mu             sync.Mutex
	grpcStream     toolv1.ToolService_InvokeServer
	closed         bool
	exitStatusSent bool
}

// newStream wraps g for use by a single Invoke call.
func newStream(g toolv1.ToolService_InvokeServer) *Stream {
	return &Stream{grpcStream: g}
}

// Context returns the Invoke call's context — cancelled by the kernel
// closing the gRPC stream (user interrupt, timeout, turn abort). A
// Provider treats this as normal control flow, never as an error
// condition to log.
func (s *Stream) Context() context.Context {
	return s.grpcStream.Context()
}

// Send sends event, enforcing the stream contract documented on Stream.
// Safe for concurrent use; concurrent Send calls serialize rather than
// racing the underlying gRPC stream.
func (s *Stream) Send(event *Event) error {
	if event == nil {
		return ErrNilEvent
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStreamClosed
	}

	if event.Result != nil {
		select {
		case <-s.grpcStream.Context().Done():
			return ErrResultAfterCancel
		default:
		}
	}

	if event.ExitStatus != nil {
		if s.exitStatusSent {
			return ErrDuplicateExitStatus
		}
		s.exitStatusSent = true
	}

	pe, err := toProtoEvent(event)
	if err != nil {
		return err
	}
	if err := s.grpcStream.Send(&toolv1.InvokeResponse{Event: pe}); err != nil {
		return err
	}

	if event.Result != nil || event.Error != nil {
		s.closed = true
	}
	return nil
}

// closedTerminal reports whether a terminal Result or Error event has
// already been sent — used by server.go to detect a Provider.Invoke that
// returned nil without ever closing the stream.
func (s *Stream) closedTerminal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
