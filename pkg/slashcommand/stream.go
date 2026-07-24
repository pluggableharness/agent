package slashcommand

import (
	"context"
	"errors"
	"sync"

	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
)

// Sentinel errors returned by Stream.Send.
var (
	// ErrStreamClosed is returned by Send once a terminal Result or
	// Error event has already been sent — the stream contract
	// (docs/specifications/slashcommand/protocol.md#invoke, reusing
	// tool/protocol.md#invoke verbatim: "exactly one of result or error
	// MUST close the stream") forbids sending anything after that.
	ErrStreamClosed = errors.New("slashcommand: invoke stream already closed by a terminal result or error event")
	// ErrResultAfterCancel is returned by Send when asked to send a
	// success Result event after the stream's context has already been
	// cancelled. Per tool/protocol.md#invoke, reused verbatim: "A
	// plugin MUST NOT synthesize a result claiming full success after a
	// cancelled operation." — send a partial_result/output_chunk
	// best-effort report, or an error event with
	// tool.ErrorCategoryCancelled, instead.
	ErrResultAfterCancel = errors.New("slashcommand: cannot send a success result after the stream's context was canceled")
	// ErrDuplicateExitStatus is returned by Send on a second
	// exit_status event within one stream.
	// docs/specifications/slashcommand/data-types.md#slashcommandcall--slashcommandevent:
	// "exit_status MAY appear at most once."
	ErrDuplicateExitStatus = errors.New("slashcommand: exit_status may appear at most once per invoke stream")
)

// Stream is the cancellation-safe sender a Provider's Invoke uses to emit
// Events, per docs/specifications/slashcommand/protocol.md#invoke. It
// mirrors pkg/tool.Stream's discipline exactly — the two categories share
// the identical stream contract per data-types.md — but is its own type
// because SlashCommandCall/SlashCommandEvent are distinct generated
// messages from ToolCall/ToolEvent, so tool.Stream cannot be reused
// directly. It enforces the parts of the Invoke stream contract that are
// mechanically checkable from the sequence of Send calls alone:
//
//   - exactly one of a terminal Result or Error event closes the stream;
//     any Send after that returns ErrStreamClosed.
//   - exit_status appears at most once; a second one returns
//     ErrDuplicateExitStatus.
//   - a success Result is refused once the stream's own context has been
//     cancelled, so a Provider cannot synthesize a false "succeeded"
//     terminal event after cancellation (ErrResultAfterCancel) — send a
//     partial_result or an error event with tool.ErrorCategoryCancelled
//     instead.
//   - output_chunk (and every other event) ordering is preserved because
//     Send serializes every call under one mutex rather than writing to
//     the underlying gRPC stream directly from more than one goroutine.
type Stream struct {
	mu             sync.Mutex
	grpcStream     slashcommandv1.SlashCommandService_InvokeServer
	closed         bool
	exitStatusSent bool
}

// newStream wraps g for use by a single Invoke call.
func newStream(g slashcommandv1.SlashCommandService_InvokeServer) *Stream {
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
	if err := s.grpcStream.Send(&slashcommandv1.InvokeResponse{Event: pe}); err != nil {
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
