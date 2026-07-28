package streamaccum

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Sentinel errors Observe returns for a structurally invalid StreamEvent
// sequence. Callers use errors.Is, never string matching.
var (
	// ErrStreamTerminated is returned when Observe is called after the
	// stream already reached a terminal event (Stop or Error) —
	// data-types.md#streamevent documents exactly one terminal event per
	// stream.
	ErrStreamTerminated = errors.New("streamaccum: event received after stream already terminated")
	// ErrEmptyEvent is returned when a StreamEvent carries no oneof
	// variant at all — the wire guarantees exactly one is set.
	ErrEmptyEvent = errors.New("streamaccum: stream event carries no variant")
	// ErrUnknownToolCall is returned when a ToolCallDelta or ToolCallDone
	// references an id no ToolCallStart ever introduced.
	ErrUnknownToolCall = errors.New("streamaccum: tool call delta/done references an unstarted id")
	// ErrDuplicateToolCall is returned when a ToolCallStart reuses an id
	// still in flight from an earlier ToolCallStart.
	ErrDuplicateToolCall = errors.New("streamaccum: tool call start id already in use")
	// ErrToolCallAlreadyDone is returned when a ToolCallDelta or a second
	// ToolCallDone arrives for an id whose ToolCallDone already fired.
	ErrToolCallAlreadyDone = errors.New("streamaccum: tool call delta/done after that call's tool_call_done")
	// ErrThinkingSignatureWithoutBlock is returned when a
	// ThinkingSignature event arrives with no thinking block currently
	// open to attach it to.
	ErrThinkingSignatureWithoutBlock = errors.New("streamaccum: thinking_signature with no open thinking block")
)

// blockKind identifies which implicit, delta-accumulated content block (if
// any) is currently open — i.e. still eligible to absorb the next delta of
// the same kind, per data-types.md#streamevent's text_delta/thinking_delta
// accumulation rules. Neither the wire format nor the canonical Message
// carries a content-block index (confirmed by reading the generated
// StreamEvent/ContentBlock types): a text or thinking block's boundaries
// are implicit in the event sequence itself — a run of same-kind deltas is
// one block, and any other event closes it. tool_use blocks need no such
// tracking: they're correlated by ToolCallStart/Delta/Done's own id field,
// so several may accumulate concurrently regardless of what text/thinking
// block is or isn't open.
type blockKind int

const (
	blockKindNone blockKind = iota
	blockKindText
	blockKindThinking
)

// toolCallState tracks one in-flight tool_use block: the ToolUseBlock
// already appended to Accumulator.blocks (so its Arguments field can be
// filled in once ToolCallDone fires) and the concatenation of every
// ToolCallDelta fragment seen so far, parsed as JSON only once, per
// examples.md's "the kernel accumulates tool_call_delta fragments by id
// into the final parsed-JSON arguments" rule.
type toolCallState struct {
	block       *contentv1.ToolUseBlock
	argsPending []byte
	done        bool
}

// Accumulator builds one canonical Message from a StreamCompletion event
// sequence, per docs/specifications/agent-loop/turn-algorithm.md#the-runturn-algorithm's
// `message := accumulate(stream)` step. Feed it every StreamEvent the model
// provider emits, in order, via Observe; once a terminal event (Stop or
// Error) has been observed, Result and Err report the accumulated outcome.
//
// Not safe for concurrent use — see this package's doc comment.
type Accumulator struct {
	blocks []*contentv1.ContentBlock

	openKind     blockKind
	openText     *contentv1.TextBlock
	openThinking *contentv1.ThinkingBlock
	// openThinkingChannel is which reasoning stream the open thinking
	// block belongs to, so a summary run and a raw-reasoning run stay
	// separate blocks even when adjacent.
	openThinkingChannel modelv1.StreamEvent_ThinkingChannel
	pendingSig          []byte
	tools               map[string]*toolCallState

	usage      *modelv1.Usage
	stopReason modelv1.StopReason
	modelErr   *modelv1.ModelError
	terminal   bool

	// providerRequestID is the vendor's own identifier for this request,
	// from a StreamStart event. Empty when the provider published none —
	// events.proto marks StreamStart as MAY-omit.
	providerRequestID string

	// correlationIDs are the other handles this request is known by,
	// from StreamStart. Nil until one arrives.
	correlationIDs map[string]string

	// metadata is every StreamMetadata event observed so far, merged
	// field by field in arrival order. Nil until one arrives.
	//
	// Merged rather than replaced because events.proto specifies a later
	// event as superseding an earlier one per field, with an absent field
	// meaning "no new information" rather than "cleared" — keeping only
	// the newest event would silently drop an actual_model reported once
	// at the top of a stream.
	metadata *modelv1.StreamEvent_StreamMetadata

	// safety are the vendor interventions reported during this stream, in
	// arrival order. Accumulated rather than replaced: a request can be
	// buffered and then moderated, and the sequence is the explanation.
	safety []*modelv1.StreamEvent_SafetyNotice
}

// New returns a ready Accumulator with no content observed yet.
func New() *Accumulator {
	return &Accumulator{
		tools: make(map[string]*toolCallState),
	}
}

// Observe feeds one StreamEvent into the accumulator, in the order the
// model provider emitted it. It returns an error only for a structurally
// invalid sequence: an event after the stream already terminated, a tool
// call delta/done referencing an id no ToolCallStart introduced (or one
// already finished), a duplicate ToolCallStart, a ThinkingSignature with no
// open thinking block, malformed tool-call-argument JSON, or a StreamEvent
// with no oneof variant set at all.
func (a *Accumulator) Observe(ev *modelv1.StreamEvent) error {
	if a.terminal {
		return ErrStreamTerminated
	}
	if ev == nil {
		return ErrEmptyEvent
	}

	switch e := ev.GetEvent().(type) {
	case *modelv1.StreamEvent_StreamStart_:
		// Purely informational, and deliberately not a block boundary:
		// it carries no content and normally arrives before any, so
		// closing an open block here would split a run of deltas that a
		// vendor happened to precede with a late StreamStart.
		a.observeStreamStart(e.StreamStart)
		return nil
	case *modelv1.StreamEvent_SafetyNotice_:
		// Not a block boundary: it carries no content, and a vendor may
		// interpose mid-completion, so closing an open block here would
		// split a text run around a moderation notice.
		a.safety = append(a.safety, e.SafetyNotice)
		return nil
	case *modelv1.StreamEvent_Metadata:
		// Not a block boundary either, and for a stronger reason:
		// events.proto explicitly permits this mid-stream, so closing an
		// open block here would split a text run whenever a vendor
		// revised its headers partway through a completion.
		a.observeMetadata(e.Metadata)
		return nil
	case *modelv1.StreamEvent_TextDelta_:
		a.observeTextDelta(e.TextDelta.GetText())
		return nil
	case *modelv1.StreamEvent_ThinkingDelta_:
		a.observeThinkingDelta(e.ThinkingDelta.GetText(), e.ThinkingDelta.GetChannel())
		return nil
	case *modelv1.StreamEvent_ThinkingSignature_:
		return a.observeThinkingSignature(e.ThinkingSignature.GetSignature())
	case *modelv1.StreamEvent_RedactedThinking_:
		a.observeRedactedThinking(e.RedactedThinking.GetData())
		return nil
	case *modelv1.StreamEvent_ToolCallStart_:
		return a.observeToolCallStart(e.ToolCallStart.GetId(), e.ToolCallStart.GetName())
	case *modelv1.StreamEvent_ToolCallDelta_:
		return a.observeToolCallDelta(e.ToolCallDelta.GetId(), e.ToolCallDelta.GetArgumentsFragment())
	case *modelv1.StreamEvent_ToolCallDone_:
		return a.observeToolCallDone(e.ToolCallDone.GetId())
	case *modelv1.StreamEvent_Usage:
		a.closeOpenBlock()
		a.usage = e.Usage
		return nil
	case *modelv1.StreamEvent_Stop_:
		a.closeOpenBlock()
		a.terminal = true
		a.stopReason = e.Stop.GetReason()
		return nil
	case *modelv1.StreamEvent_Error_:
		a.closeOpenBlock()
		a.terminal = true
		a.modelErr = e.Error.GetError()
		return nil
	case nil:
		return ErrEmptyEvent
	default:
		return fmt.Errorf("streamaccum: unhandled StreamEvent variant %T", e)
	}
}

// Result returns the fully accumulated Message plus the final Usage and
// StopReason once the stream has reached its terminal event (a Stop or an
// Error variant). ok is false if the stream hasn't reached a terminal event
// yet — a caller mid-stream should not call this. The Message's Content
// blocks appear in the order the model streamed them (the order each block
// was first opened), never observation order for any later event touching
// an already-open block.
func (a *Accumulator) Result() (msg *contentv1.Message, usage *modelv1.Usage, stop modelv1.StopReason, ok bool) {
	if !a.terminal {
		return nil, nil, modelv1.StopReason_STOP_REASON_UNSPECIFIED, false
	}
	return &contentv1.Message{
		Role:    contentv1.Role_ROLE_ASSISTANT,
		Content: a.blocks,
	}, a.usage, a.stopReason, true
}

// Err returns the terminal ModelError if the stream ended in an Error
// variant rather than a Stop variant, nil otherwise (including when the
// stream hasn't terminated yet).
func (a *Accumulator) Err() *modelv1.ModelError {
	return a.modelErr
}

// observeStreamStart records the vendor's handles for this request.
//
// A second StreamStart overwrites rather than merges: the event means
// "the vendor accepted this request and named it", and two different
// names for one request is a provider bug, not a merge to reconcile.
func (a *Accumulator) observeStreamStart(ev *modelv1.StreamEvent_StreamStart) {
	a.providerRequestID = ev.GetProviderRequestId()
	if ids := ev.GetCorrelationIds(); len(ids) > 0 {
		a.correlationIDs = maps.Clone(ids)
	}
}

// observeMetadata merges one StreamMetadata event into the accumulated
// view, field by field.
//
// Absent means "no new information", never "cleared" — so every field
// below is copied only when the incoming event actually set it. The
// repeated and map fields follow the same rule: a non-empty value
// replaces (rate_limits is a whole snapshot of every budget, so merging
// entry by entry would resurrect a budget the vendor stopped reporting),
// while attrs merges by key (it is an open bag of independent facts).
func (a *Accumulator) observeMetadata(ev *modelv1.StreamEvent_StreamMetadata) {
	if a.metadata == nil {
		a.metadata = &modelv1.StreamEvent_StreamMetadata{}
	}
	m := a.metadata

	if v := ev.ActualModel; v != nil {
		m.ActualModel = proto.String(*v)
	}
	if v := ev.SystemFingerprint; v != nil {
		m.SystemFingerprint = proto.String(*v)
	}
	if v := ev.ServiceTier; v != nil {
		m.ServiceTier = proto.String(*v)
	}
	if v := ev.LiveContextWindow; v != nil {
		m.LiveContextWindow = proto.Int64(*v)
	}
	if v := ev.LiveMaxOutputTokens; v != nil {
		m.LiveMaxOutputTokens = proto.Int64(*v)
	}
	if v := ev.CatalogEtag; v != nil {
		m.CatalogEtag = proto.String(*v)
	}
	if len(ev.GetRateLimits()) > 0 {
		m.RateLimits = ev.GetRateLimits()
	}
	for k, v := range ev.GetAttrs() {
		if m.Attrs == nil {
			m.Attrs = make(map[string]string, len(ev.GetAttrs()))
		}
		m.Attrs[k] = v
	}
}

// Metadata returns every StreamMetadata event observed so far, merged
// into one value, or nil if the provider reported none.
//
// Readable mid-stream by design: actual_model and the rate-limit
// snapshots it carries are most useful while a completion is still
// running, which is the reason the event exists separately from the
// terminal usage payload.
//
// The returned message aliases the accumulator's own state — treat it as
// read-only, exactly as Result's Message and Usage are.
func (a *Accumulator) Metadata() *modelv1.StreamEvent_StreamMetadata {
	return a.metadata
}

// CorrelationIDs returns the additional vendor handles for this request
// from StreamStart, or nil if none were reported. The returned map is a
// copy: a caller stashing it for a support ticket must not be able to
// mutate what a later Result reports.
func (a *Accumulator) CorrelationIDs() map[string]string {
	return maps.Clone(a.correlationIDs)
}

// SafetyNotices returns the vendor interventions reported during this
// stream, in arrival order, or nil if there were none.
//
// Readable mid-stream deliberately: a BUFFERING notice explains a stall
// while the stall is still happening, which is the only time the
// explanation is worth anything.
//
// The returned slice aliases the accumulator's own state — read-only,
// like Result's Message and Usage.
func (a *Accumulator) SafetyNotices() []*modelv1.StreamEvent_SafetyNotice {
	return a.safety
}

// ProviderRequestID returns the vendor's own identifier for this request,
// as carried by a StreamStart event, or "" if the provider published
// none. It is opaque: surfaced so a failure can be correlated against the
// vendor's logs, never parsed.
//
// Unlike Result, this is readable mid-stream — the id arrives early
// precisely so it is available when the stream fails rather than only
// when it succeeds.
func (a *Accumulator) ProviderRequestID() string {
	return a.providerRequestID
}

// closeOpenBlock finalizes whatever implicit text/thinking block is
// currently open, per data-types.md#streamevent: a thinking block's
// signature is "attached... once, at that block's own terminal point,"
// which this package treats as the moment a different kind of event
// arrives (or the stream terminates) — see blockKind's doc comment for why
// there's no explicit index to detect this some other way. A no-op when no
// block is open.
func (a *Accumulator) closeOpenBlock() {
	if a.openKind == blockKindThinking && len(a.pendingSig) > 0 {
		a.openThinking.Signature = a.pendingSig
	}
	a.openKind = blockKindNone
	a.openText = nil
	a.openThinking = nil
	a.pendingSig = nil
}

// observeTextDelta appends text to the currently open text block, opening
// a new one first if the previous event wasn't itself a TextDelta
// continuing the same block.
func (a *Accumulator) observeTextDelta(text string) {
	if a.openKind != blockKindText {
		a.closeOpenBlock()
		tb := &contentv1.TextBlock{}
		a.blocks = append(a.blocks, &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Text{Text: tb}})
		a.openText = tb
		a.openKind = blockKindText
	}
	a.openText.Text += text
}

// observeThinkingDelta appends reasoning text to the currently open
// thinking block, opening a new one first if the previous event wasn't
// itself a ThinkingDelta (or a ThinkingSignature belonging to the same
// block) continuing it.
func (a *Accumulator) observeThinkingDelta(text string, channel modelv1.StreamEvent_ThinkingChannel) {
	// A channel switch closes the open block even though both sides are
	// thinking. A vendor-written summary and the raw reasoning it
	// summarizes are different text; concatenating them because they are
	// adjacent and both "thinking" would produce one block that reads as
	// neither. Providers that set no channel leave this UNSPECIFIED
	// throughout, so their runs coalesce exactly as before.
	if a.openKind != blockKindThinking || a.openThinkingChannel != channel {
		a.closeOpenBlock()
		th := &contentv1.ThinkingBlock{}
		a.blocks = append(a.blocks, &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Thinking{Thinking: th}})
		a.openThinking = th
		a.openKind = blockKindThinking
		a.openThinkingChannel = channel
	}
	a.openThinking.Text += text
}

// observeThinkingSignature accumulates signature bytes onto the currently
// open thinking block's pending signature buffer — stored and returned
// verbatim, never decoded or reinterpreted, per this package's doc comment
// and CLAUDE.md. Attached to the ThinkingBlock only once closeOpenBlock
// finalizes the block. Errors if no thinking block is currently open: a
// signature with nothing to attach to is a structurally invalid sequence.
func (a *Accumulator) observeThinkingSignature(sig []byte) error {
	if a.openKind != blockKindThinking {
		return ErrThinkingSignatureWithoutBlock
	}
	a.pendingSig = append(a.pendingSig, sig...)
	return nil
}

// observeRedactedThinking appends a complete RedactedThinkingBlock. Unlike
// thinking_delta, redacted_thinking is never fragmented across events
// (data-types.md#streamevent, events.proto's RedactedThinking doc comment)
// — the whole opaque payload arrives in this one call, so there is no
// accumulation state to track afterward.
func (a *Accumulator) observeRedactedThinking(data []byte) {
	a.closeOpenBlock()
	a.blocks = append(a.blocks, &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_RedactedThinking{
			RedactedThinking: &contentv1.RedactedThinkingBlock{Data: data},
		},
	})
}

// observeToolCallStart opens a new tool_use block for id, closing whatever
// implicit text/thinking block was open. Errors if id is already in flight
// from an earlier, not-yet-done ToolCallStart.
func (a *Accumulator) observeToolCallStart(id, name string) error {
	if _, exists := a.tools[id]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateToolCall, id)
	}
	a.closeOpenBlock()

	block := &contentv1.ToolUseBlock{Id: id, Name: name}
	a.blocks = append(a.blocks, &contentv1.ContentBlock{Block: &contentv1.ContentBlock_ToolUse{ToolUse: block}})
	a.tools[id] = &toolCallState{block: block}
	return nil
}

// observeToolCallDelta appends fragment to id's pending-arguments buffer.
// Fragments are concatenated and parsed as one JSON document only once
// ToolCallDone fires — per examples.md, never parsed fragment-by-fragment.
// Errors if id was never started, or already finished via ToolCallDone.
func (a *Accumulator) observeToolCallDelta(id, fragment string) error {
	ts, ok := a.tools[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownToolCall, id)
	}
	if ts.done {
		return fmt.Errorf("%w: %q", ErrToolCallAlreadyDone, id)
	}
	ts.argsPending = append(ts.argsPending, fragment...)
	return nil
}

// observeToolCallDone parses id's accumulated argument fragments as one
// JSON document and stores the result on the ToolUseBlock. An id with no
// fragments at all (a no-argument tool call) gets a nil Arguments, mirroring
// pkg/tool/convert.go's "absence of a payload is a meaningful, documented
// zero value" convention rather than an empty-but-non-nil Struct. Errors if
// id was never started, already finished, or its accumulated fragments
// don't parse as valid JSON.
func (a *Accumulator) observeToolCallDone(id string) error {
	ts, ok := a.tools[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownToolCall, id)
	}
	if ts.done {
		return fmt.Errorf("%w: %q", ErrToolCallAlreadyDone, id)
	}

	if len(ts.argsPending) > 0 {
		var m map[string]any
		if err := json.Unmarshal(ts.argsPending, &m); err != nil {
			return fmt.Errorf("streamaccum: tool call %q: parse accumulated arguments: %w", id, err)
		}
		if len(m) > 0 {
			s, err := structpb.NewStruct(m)
			if err != nil {
				return fmt.Errorf("streamaccum: tool call %q: encode arguments: %w", id, err)
			}
			ts.block.Arguments = s
		}
	}
	ts.done = true
	return nil
}
