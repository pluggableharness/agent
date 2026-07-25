package model

import (
	"context"
	"sync"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Sink is the cancellation-safe StreamCompletion event writer handed to
// Provider.StreamCompletion. Exactly one terminal event — Stop or Error —
// may be sent per stream, per docs/specifications/model/data-types.md#streamevent;
// a call after the first terminal event returns ErrStreamAlreadyTerminated
// without touching the wire. Safe for concurrent use, though a Provider
// implementation typically drives it from a single goroutine.
type Sink struct {
	stream modelv1.ModelService_StreamCompletionServer

	mu       sync.Mutex
	terminal bool
}

// newSink wraps stream. Unexported: a Provider never constructs a Sink
// itself, only receives one from server.go's StreamCompletion handler.
func newSink(stream modelv1.ModelService_StreamCompletionServer) *Sink {
	return &Sink{stream: stream}
}

// Context returns the stream's context — cancelled when the kernel closes
// the gRPC stream (docs/specifications/model/README.md#transport--lifecycle).
// A Provider's StreamCompletion loop SHOULD select on this (or the ctx
// argument it was called with, which is the same context) to stop
// generating promptly. Not cached on Sink itself
// (.claude/rules/go-architecture.md's "never store a context.Context in a
// struct field") — grpc.ServerStream.Context() is a cheap getter, so
// re-deriving it per call costs nothing.
func (s *Sink) Context() context.Context {
	return s.stream.Context()
}

// send writes ev, honoring the terminal-event and cancellation invariants
// documented on Sink. terminal marks ev itself as the stream's terminal
// event when true (Stop, Error) — recorded before the write is attempted,
// so a failed or racing second terminal call never gets a chance to send.
func (s *Sink) send(ev *modelv1.StreamEvent, terminal bool) error {
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return ErrStreamAlreadyTerminated
	}
	if terminal {
		s.terminal = true
	}
	s.mu.Unlock()

	if err := s.stream.Context().Err(); err != nil {
		// Cancellation is normal control flow, never an error condition —
		// docs/specifications/model/README.md#transport--lifecycle,
		// .claude/rules/grpc.md's cancellation rule. Returned as-is so a
		// caller's errors.Is(err, context.Canceled) works.
		return err
	}
	return s.stream.Send(ev)
}

// TextDelta sends an incremental fragment of assistant text output. MUST
// be supported by every plugin.
func (s *Sink) TextDelta(text string) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_TextDelta_{
			TextDelta: &modelv1.StreamEvent_TextDelta{Text: text},
		},
	}, false)
}

// ThinkingDelta sends an incremental fragment of the model's reasoning
// output. Only meaningful when the target model's ThinkingSpec.Supported.
func (s *Sink) ThinkingDelta(text string) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_ThinkingDelta_{
			ThinkingDelta: &modelv1.StreamEvent_ThinkingDelta{Text: text},
		},
	}, false)
}

// ThinkingSignature sends the vendor's opaque integrity token for the
// reasoning block just completed. MUST be sent if the vendor's thinking
// blocks carry an integrity signature — the kernel stores and echoes it
// back verbatim, never inspecting or reformatting it.
func (s *Sink) ThinkingSignature(signature []byte) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_ThinkingSignature_{
			ThinkingSignature: &modelv1.StreamEvent_ThinkingSignature{Signature: signature},
		},
	}, false)
}

// RedactedThinking sends one complete, vendor-encrypted reasoning block.
//
// Unlike ThinkingDelta this is never fragmented: the vendor emits the
// block whole because its contents are deliberately opaque, so there is
// nothing to accumulate. A plugin MUST emit this whenever its vendor
// produces reasoning content it requires be echoed back verbatim on a
// later turn (docs/specifications/model/conformance.md's
// StreamEvent.redacted_thinking row); the kernel stores and round-trips
// it into ContentBlock's RedactedThinkingBlock.data without inspecting
// it. Only meaningful when the target model's ThinkingSpec.Supported.
//
// data is passed through byte-for-byte. A Provider MUST NOT decode and
// re-encode a vendor's base64 payload on the way through: a re-encoding
// that differs in padding or alphabet from the vendor's own makes the
// block fail the vendor's integrity check on the next turn, which
// typically rejects the whole conversation rather than just the block.
func (s *Sink) RedactedThinking(data []byte) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_RedactedThinking_{
			RedactedThinking: &modelv1.StreamEvent_RedactedThinking{Data: data},
		},
	}, false)
}

// ToolCallStart announces the model has begun requesting a tool
// invocation. id correlates the matching ToolCallDelta/ToolCallDone calls
// and the resulting ToolUseBlock.id; name is the tool's declared name.
func (s *Sink) ToolCallStart(id, name string) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_ToolCallStart_{
			ToolCallStart: &modelv1.StreamEvent_ToolCallStart{Id: id, Name: name},
		},
	}, false)
}

// ToolCallDelta sends one incremental fragment of a tool call's arguments,
// accumulated by the kernel across deltas into the final parsed JSON.
func (s *Sink) ToolCallDelta(id, argumentsFragment string) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_ToolCallDelta_{
			ToolCallDelta: &modelv1.StreamEvent_ToolCallDelta{Id: id, ArgumentsFragment: argumentsFragment},
		},
	}, false)
}

// ToolCallDone signals a tool call's arguments are complete and ready for
// the kernel to parse and dispatch.
func (s *Sink) ToolCallDone(id string) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_ToolCallDone_{
			ToolCallDone: &modelv1.StreamEvent_ToolCallDone{Id: id},
		},
	}, false)
}

// Usage sends token accounting for this completion. The kernel computes
// and persists cost_usd from these counts plus the matching PricingTier —
// a Provider never computes cost itself
// (docs/specifications/model/protocol.md#cost-computation).
func (s *Sink) Usage(u Usage) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_Usage{Usage: usageToProto(u)},
	}, false)
}

// Stop sends the stream's terminal Stop event and closes the sink to
// further sends. matchedStopSequence MUST be non-empty iff
// reason == STOP_REASON_STOP_SEQUENCE; Stop ignores it (leaves it unset on
// the wire) for every other reason rather than erroring, since a Provider
// passing a stray value alongside an unrelated reason is a harmless
// authoring mistake, not a wire-protocol violation worth failing the whole
// stream over.
func (s *Sink) Stop(reason modelv1.StopReason, matchedStopSequence string) error {
	stop := &modelv1.StreamEvent_Stop{Reason: reason}
	if reason == modelv1.StopReason_STOP_REASON_STOP_SEQUENCE && matchedStopSequence != "" {
		seq := matchedStopSequence
		stop.MatchedStopSequence = &seq
	}
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_Stop_{Stop: stop},
	}, true)
}

// Error sends the stream's terminal Error event — a plugin classifying a
// failure *within* an otherwise-open stream, per
// docs/specifications/model/data-types.md#streamevent, distinct from the
// stream being torn down at the transport level. A backend that fails
// outright before producing any events MAY call this with no preceding
// event at all.
func (s *Sink) Error(modelErr *Error) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_Error_{
			Error: &modelv1.StreamEvent_Error{Error: modelErr.toProto()},
		},
	}, true)
}
