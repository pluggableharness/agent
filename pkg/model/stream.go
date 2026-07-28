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

// StreamStart sends the vendor's own identifier for this request, so a
// later failure can be correlated with the vendor's logs.
//
// SHOULD be called as soon as the id is known — normally from response
// headers, before any content streams — and before any other event. An id
// that only arrives on successful completion is absent in exactly the case
// it is needed, which is why this is a separate early event rather than a
// field on Stop. A Provider whose vendor publishes no such id simply never
// calls this.
//
// When the vendor also publishes secondary handles (x-request-id, cf-ray,
// a response id distinct from the stream id), use StreamStartWith so
// support tooling can look the request up under any of them.
func (s *Sink) StreamStart(providerRequestID string) error {
	return s.StreamStartWith(providerRequestID, nil)
}

// StreamStartWith is StreamStart plus every other handle the vendor uses
// for the same request, keyed by the vendor's own name for it
// ("x-request-id", "response_id", "cf-ray").
//
// providerRequestID remains the single canonical handle on the wire;
// correlationIDs carries the rest rather than forcing the adapter to
// discard them. A nil or empty map is equivalent to StreamStart.
//
// The kernel sorts map keys before persisting
// (docs/specifications/model/data-types.md; .claude/rules/determinism.md);
// adapters need not pre-sort.
func (s *Sink) StreamStartWith(providerRequestID string, correlationIDs map[string]string) error {
	start := &modelv1.StreamEvent_StreamStart{ProviderRequestId: providerRequestID}
	if len(correlationIDs) > 0 {
		// Copy so a caller reusing the map cannot mutate an in-flight send.
		cp := make(map[string]string, len(correlationIDs))
		for k, v := range correlationIDs {
			if k == "" {
				continue
			}
			cp[k] = v
		}
		if len(cp) > 0 {
			start.CorrelationIds = cp
		}
	}
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_StreamStart_{StreamStart: start},
	}, false)
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

// Metadata sends non-content facts about how the vendor is serving this
// request: which model actually answered, which build, which tier, and
// whatever budget state the response headers exposed.
//
// MAY be sent more than once and at any point before the terminal event.
// A later call supersedes an earlier one field by field; leaving a field
// nil means "no new information", never "cleared", so a provider can
// report actual_model once at the top of a stream and rate limits later
// without the first being lost.
//
// Send this as soon as the facts are known rather than saving them for
// the end — an operator learning that a limit was nearly exhausted only
// after the turn that exhausted it has learned nothing useful.
func (s *Sink) Metadata(m StreamMetadata) error {
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_Metadata{Metadata: &modelv1.StreamEvent_StreamMetadata{
			ActualModel:         m.ActualModel,
			SystemFingerprint:   m.SystemFingerprint,
			ServiceTier:         m.ServiceTier,
			RateLimits:          rateLimitsToProto(m.RateLimits),
			LiveContextWindow:   m.LiveContextWindow,
			LiveMaxOutputTokens: m.LiveMaxOutputTokens,
			CatalogEtag:         m.CatalogEtag,
			StickyTurnToken:     m.StickyTurnToken,
			Attrs:               m.Attrs,
		}},
	}, false)
}

// SafetyNotice reports that the vendor is interposing on this request —
// buffering output for review, applying a moderation decision, or
// requiring an account challenge.
//
// Send it when the vendor says so, particularly for BUFFERING: it is what
// lets a frontend explain a stall instead of leaving it looking like a
// hang.
func (s *Sink) SafetyNotice(kind modelv1.StreamEvent_SafetyKind, message string, attrs map[string]string) error {
	notice := &modelv1.StreamEvent_SafetyNotice{Kind: kind, Attrs: attrs}
	if message != "" {
		notice.Message = &message
	}
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_SafetyNotice_{SafetyNotice: notice},
	}, false)
}

// ThinkingDeltaOn sends a reasoning fragment tagged with which stream it
// belongs to, for vendors emitting a readable summary alongside raw
// reasoning.
//
// Use this instead of ThinkingDelta when the vendor distinguishes the
// two: the kernel keeps a summary run and a content run as separate
// blocks, and sending both through the untagged ThinkingDelta would
// concatenate them into one block that reads as neither.
//
// When the vendor also numbers parallel reasoning parts within a channel,
// use ThinkingDeltaOnPart so fragments of different parts are not
// concatenated into one block.
func (s *Sink) ThinkingDeltaOn(text string, channel modelv1.StreamEvent_ThinkingChannel) error {
	return s.thinkingDelta(text, channel, nil)
}

// ThinkingDeltaOnPart is ThinkingDeltaOn with a vendor-supplied part index.
//
// Fragments that share a (channel, partIndex) pair are one reasoning block;
// a different partIndex starts a new block within the same channel. Use
// this only when the vendor actually numbers parts — inventing indices
// would invent block boundaries the kernel cannot later re-split.
func (s *Sink) ThinkingDeltaOnPart(text string, channel modelv1.StreamEvent_ThinkingChannel, partIndex int32) error {
	idx := partIndex
	return s.thinkingDelta(text, channel, &idx)
}

func (s *Sink) thinkingDelta(text string, channel modelv1.StreamEvent_ThinkingChannel, partIndex *int32) error {
	delta := &modelv1.StreamEvent_ThinkingDelta{Text: text, Channel: channel}
	if partIndex != nil {
		delta.PartIndex = partIndex
	}
	return s.send(&modelv1.StreamEvent{
		Event: &modelv1.StreamEvent_ThinkingDelta_{ThinkingDelta: delta},
	}, false)
}

// StreamMetadata is Sink.Metadata's payload. Every field is optional: a
// provider reports what its vendor published and leaves the rest nil.
type StreamMetadata struct {
	// ActualModel is the model that actually served this completion, when
	// it differs from the requested id. Set it whenever the vendor says
	// so — it is what makes a silent model substitution attributable.
	ActualModel *string
	// SystemFingerprint is the vendor's opaque backend-build identifier.
	SystemFingerprint *string
	// ServiceTier is the tier this request was served at.
	ServiceTier *string
	// RateLimits is budget state as of this point in the stream, from
	// response headers.
	RateLimits []RateLimitSnapshot
	// LiveContextWindow is the context window the vendor says applies to
	// this request, superseding the roster's static figure.
	LiveContextWindow *int64
	// LiveMaxOutputTokens is the same for maximum output tokens.
	LiveMaxOutputTokens *int64
	// CatalogEtag is the vendor's current model-catalog version.
	CatalogEtag *string
	// StickyTurnToken is a handle to this turn's vendor-side state, for
	// vendors accepting an incremental continuation next request.
	StickyTurnToken *string
	// Attrs are vendor-defined metadata with no typed field.
	Attrs map[string]string
}
