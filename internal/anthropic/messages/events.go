package messages

import (
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// EventSink is the subset of *model.Sink this package writes to. It exists
// so Translator can be tested against a recording fake without a live gRPC
// stream; *model.Sink satisfies it structurally.
type EventSink interface {
	// StreamStart sends the vendor's own identifier for this request.
	StreamStart(providerRequestID string) error
	// TextDelta sends an incremental fragment of assistant text output.
	TextDelta(text string) error
	// ThinkingDelta sends an incremental fragment of the model's reasoning
	// output.
	ThinkingDelta(text string) error
	// ThinkingSignature sends the vendor's opaque integrity token for the
	// reasoning block just completed.
	ThinkingSignature(signature []byte) error
	// RedactedThinking sends one complete, vendor-encrypted reasoning
	// block.
	RedactedThinking(data []byte) error
	// ToolCallStart announces the model has begun requesting a tool
	// invocation.
	ToolCallStart(id, name string) error
	// ToolCallDelta sends one incremental fragment of a tool call's
	// arguments.
	ToolCallDelta(id, argumentsFragment string) error
	// ToolCallDone signals a tool call's arguments are complete.
	ToolCallDone(id string) error
	// Usage sends token accounting for this completion.
	Usage(u model.Usage) error
	// Stop sends the stream's terminal Stop event.
	Stop(reason modelv1.StopReason, matchedStopSequence string) error
	// Error sends the stream's terminal Error event.
	Error(modelErr *model.Error) error
}

// Compile-time proof the real Sink satisfies the seam.
var _ EventSink = (*model.Sink)(nil)

// Translator converts Anthropic's stream events into Sink calls, per
// docs/specifications/model/examples.md's worked StreamCompletion event
// sequence.
//
// A Translator is single-use: construct one per StreamCompletion call with
// NewTranslator and feed it every decoded StreamEvent, in order, via
// Handle.
type Translator struct {
	sink EventSink

	// toolIndex maps a content_block index to the tool_use id declared at
	// its content_block_start, forgotten again at the matching
	// content_block_stop. input_json_delta and content_block_stop only
	// carry the index, not the id, so this is how they're reunited with
	// the ToolCallStart id ToolCallDelta/ToolCallDone require.
	toolIndex map[int64]string

	// usage accumulates the cumulative usage Anthropic reports across
	// message_start and message_delta. Anthropic's message_delta.usage is
	// cumulative, not incremental, so emitting once at message_stop with
	// the merged counts is correct — emitting at every event that carries
	// a usage object would double-count.
	usage     model.Usage
	usageSeen bool

	stopReason   modelv1.StopReason
	stopSequence string
}

// NewTranslator returns a Translator that writes to sink.
func NewTranslator(sink EventSink) *Translator {
	return &Translator{
		sink: sink,
		// STOP_REASON_END_TURN is the documented default for an
		// unknown/empty stop reason (see mapStopReason) — set here too so
		// a message_stop arriving without a preceding message_delta (not
		// expected by the protocol, but not fatal either) still reports a
		// defined reason rather than STOP_REASON_UNSPECIFIED.
		stopReason: modelv1.StopReason_STOP_REASON_END_TURN,
	}
}

// Handle processes one vendor event. It returns true once a terminal event
// (Stop or Error) has been emitted to the sink — the caller MUST stop
// feeding further events once done is true.
func (t *Translator) Handle(ev StreamEvent) (done bool, err error) {
	switch ev.Type {
	case eventMessageStart:
		if ev.Message != nil {
			t.mergeUsage(ev.Message.Usage)
		}
		return false, nil

	case eventContentBlockStart:
		return false, t.handleContentBlockStart(ev)

	case eventContentBlockDelta:
		return false, t.handleContentBlockDelta(ev)

	case eventContentBlockStop:
		return false, t.handleContentBlockStop(ev)

	case eventMessageDelta:
		t.mergeUsage(ev.Usage)
		if ev.Delta != nil {
			t.stopReason = mapStopReason(ev.Delta.StopReason)
			t.stopSequence = ev.Delta.StopSequence
		}
		return false, nil

	case eventMessageStop:
		if t.usageSeen {
			if err := t.sink.Usage(t.usage); err != nil {
				return true, err
			}
		}
		if err := t.sink.Stop(t.stopReason, t.stopSequence); err != nil {
			return true, err
		}
		return true, nil

	case eventError:
		var body APIErrorBody
		if ev.Error != nil {
			body = *ev.Error
		}
		if err := t.sink.Error(classifyStreamError(body)); err != nil {
			return true, err
		}
		return true, nil

	case eventPing:
		// Surfaced by Scanner, ignored here — see sse.go's package
		// comment for why that split exists.
		return false, nil

	default:
		// An event type this adapter doesn't recognize MUST NOT break the
		// stream (versioning policy: forward compatibility with vendor
		// additions).
		return false, nil
	}
}

// handleContentBlockStart processes a content_block_start event.
func (t *Translator) handleContentBlockStart(ev StreamEvent) error {
	block := ev.ContentBlock
	if block == nil {
		return nil
	}
	switch block.Type {
	case blockToolUse:
		if t.toolIndex == nil {
			t.toolIndex = make(map[int64]string)
		}
		t.toolIndex[ev.Index] = block.ID
		return t.sink.ToolCallStart(block.ID, block.Name)

	case blockRedactedThinking:
		// The vendor emits this block whole, never fragmented, and its
		// base64 payload is passed through byte-for-byte: decoding and
		// re-encoding it here would produce a payload that differs in
		// padding or alphabet from the vendor's own, which fails the
		// vendor's integrity check on a later turn.
		return t.sink.RedactedThinking([]byte(block.Data))

	default:
		// text and thinking need no action here — their content arrives
		// via content_block_delta. Any other block type (server_tool_use,
		// web_search_tool_result, ...) is a server-tool artifact this
		// adapter never declares and therefore never needs to act on;
		// ignoring it keeps a future vendor addition from breaking the
		// stream.
		return nil
	}
}

// handleContentBlockDelta processes a content_block_delta event.
func (t *Translator) handleContentBlockDelta(ev StreamEvent) error {
	delta := ev.Delta
	if delta == nil {
		return nil
	}
	switch delta.Type {
	case deltaText:
		return t.sink.TextDelta(delta.Text)

	case deltaThinking:
		return t.sink.ThinkingDelta(delta.Thinking)

	case deltaSignature:
		// Literal bytes of the vendor's base64 signature string, never
		// decoded/re-encoded — same integrity-preservation reason as
		// RedactedThinking above.
		return t.sink.ThinkingSignature([]byte(delta.Signature))

	case deltaInputJSON:
		id, ok := t.toolIndex[ev.Index]
		if !ok {
			return nil
		}
		return t.sink.ToolCallDelta(id, delta.PartialJSON)

	default:
		return nil
	}
}

// handleContentBlockStop processes a content_block_stop event.
func (t *Translator) handleContentBlockStop(ev StreamEvent) error {
	id, ok := t.toolIndex[ev.Index]
	if !ok {
		return nil
	}
	delete(t.toolIndex, ev.Index)
	return t.sink.ToolCallDone(id)
}

// mergeUsage folds u into t.usage. Anthropic's usage counts arrive
// piecemeal across message_start (input/cache) and message_delta (output,
// and sometimes input/cache again) — this merges by taking, per field,
// whichever event most recently supplied a non-nil value, which
// simultaneously satisfies "input/cache from whichever event supplied
// them" and "output from the last one seen" for every field. u == nil
// (an event with no usage object at all) is a no-op.
func (t *Translator) mergeUsage(u *Usage) {
	if u == nil {
		return
	}
	t.usageSeen = true
	if u.InputTokens != nil {
		t.usage.InputTokens = *u.InputTokens
	}
	if u.OutputTokens != nil {
		t.usage.OutputTokens = *u.OutputTokens
	}
	if u.CacheReadInputTokens != nil {
		v := *u.CacheReadInputTokens
		t.usage.CacheReadTokens = &v
	}
	if u.CacheCreationInputTokens != nil {
		v := *u.CacheCreationInputTokens
		t.usage.CacheWriteTokens = &v
	}
	// ReasoningTokens is deliberately left nil: Anthropic folds thinking
	// tokens into output_tokens and reports no separate figure, and a
	// vendor with no distinct count leaves the field unset rather than
	// deriving one (model.Usage's own doc comment).
}

// mapStopReason converts Anthropic's stop_reason wire string to the
// protocol's modelv1.StopReason enum. Unknown or empty input maps to
// STOP_REASON_END_TURN, the documented safe default.
func mapStopReason(reason string) modelv1.StopReason {
	switch reason {
	case stopEndTurn:
		return modelv1.StopReason_STOP_REASON_END_TURN
	case stopToolUse:
		return modelv1.StopReason_STOP_REASON_TOOL_USE
	case stopMaxTokens:
		return modelv1.StopReason_STOP_REASON_MAX_TOKENS
	case stopStopSequence:
		return modelv1.StopReason_STOP_REASON_STOP_SEQUENCE
	case stopRefusal:
		return modelv1.StopReason_STOP_REASON_REFUSAL
	case stopPauseTurn:
		// pause_turn means the vendor paused a server-tool loop; this
		// adapter declares no server tools, so it should not occur in
		// practice, and END_TURN is the safe reading if it ever does.
		return modelv1.StopReason_STOP_REASON_END_TURN
	default:
		return modelv1.StopReason_STOP_REASON_END_TURN
	}
}
