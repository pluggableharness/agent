package streamaccum

import (
	"encoding/json"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// observeAll feeds every event in evs into a fresh Accumulator and fails
// the test immediately if any Observe call errors.
func observeAll(t *testing.T, evs []*modelv1.StreamEvent) *Accumulator {
	t.Helper()
	a := New()
	for i, ev := range evs {
		if err := a.Observe(ev); err != nil {
			t.Fatalf("Observe(%d) = %v, want nil", i, err)
		}
	}
	return a
}

func textDelta(text string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_TextDelta_{TextDelta: &modelv1.StreamEvent_TextDelta{Text: text}}}
}

func streamStart(id string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_StreamStart_{StreamStart: &modelv1.StreamEvent_StreamStart{ProviderRequestId: id}}}
}

func thinkingDelta(text string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ThinkingDelta_{ThinkingDelta: &modelv1.StreamEvent_ThinkingDelta{Text: text}}}
}

func thinkingSignature(sig []byte) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ThinkingSignature_{ThinkingSignature: &modelv1.StreamEvent_ThinkingSignature{Signature: sig}}}
}

func redactedThinking(data []byte) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_RedactedThinking_{RedactedThinking: &modelv1.StreamEvent_RedactedThinking{Data: data}}}
}

func toolCallStart(id, name string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ToolCallStart_{ToolCallStart: &modelv1.StreamEvent_ToolCallStart{Id: id, Name: name}}}
}

func toolCallDelta(id, fragment string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ToolCallDelta_{ToolCallDelta: &modelv1.StreamEvent_ToolCallDelta{Id: id, ArgumentsFragment: fragment}}}
}

func toolCallDone(id string) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ToolCallDone_{ToolCallDone: &modelv1.StreamEvent_ToolCallDone{Id: id}}}
}

func usageEvent(u *modelv1.Usage) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Usage{Usage: u}}
}

func stopEvent(reason modelv1.StopReason, matched string) *modelv1.StreamEvent {
	s := &modelv1.StreamEvent_Stop{Reason: reason}
	if matched != "" {
		s.MatchedStopSequence = &matched
	}
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Stop_{Stop: s}}
}

func errorEvent(modelErr *modelv1.ModelError) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Error_{Error: &modelv1.StreamEvent_Error{Error: modelErr}}}
}

func int64ptr(v int64) *int64 { return &v }

// mustStruct builds a *structpb.Struct from a plain map, failing the test
// on error — a test-only convenience, never used by the package itself.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct(%v) = %v, want nil error", m, err)
	}
	return s
}

// TestFullWorkedExample transcribes
// docs/specifications/model/examples.md#a-full-streamcompletion-event-sequence's
// first event sequence: text, then one tool call, then usage and stop.
func TestFullWorkedExample(t *testing.T) {
	t.Parallel()

	evs := []*modelv1.StreamEvent{
		textDelta("Let me check "),
		textDelta("that file."),
		toolCallStart("tc_1", "read_file"),
		toolCallDelta("tc_1", `{"path":`),
		toolCallDelta("tc_1", `"main.go"}`),
		toolCallDone("tc_1"),
		usageEvent(&modelv1.Usage{InputTokens: 412, OutputTokens: 28}),
		stopEvent(modelv1.StopReason_STOP_REASON_TOOL_USE, ""),
	}
	a := observeAll(t, evs)

	msg, usage, stop, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if a.Err() != nil {
		t.Fatalf("Err() = %v, want nil", a.Err())
	}

	if msg.GetRole() != contentv1.Role_ROLE_ASSISTANT {
		t.Errorf("Role = %v, want ROLE_ASSISTANT", msg.GetRole())
	}
	if len(msg.GetContent()) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(msg.GetContent()))
	}

	text := msg.GetContent()[0].GetText()
	if text == nil {
		t.Fatalf("Content[0] is not a TextBlock: %+v", msg.GetContent()[0])
	}
	if want := "Let me check that file."; text.GetText() != want {
		t.Errorf("Content[0].Text = %q, want %q", text.GetText(), want)
	}

	tool := msg.GetContent()[1].GetToolUse()
	if tool == nil {
		t.Fatalf("Content[1] is not a ToolUseBlock: %+v", msg.GetContent()[1])
	}
	if tool.GetId() != "tc_1" {
		t.Errorf("Content[1].Id = %q, want tc_1", tool.GetId())
	}
	if tool.GetName() != "read_file" {
		t.Errorf("Content[1].Name = %q, want read_file", tool.GetName())
	}
	wantArgs := mustStruct(t, map[string]any{"path": "main.go"})
	if diff := structDiff(tool.GetArguments(), wantArgs); diff != "" {
		t.Errorf("Content[1].Arguments mismatch: %s", diff)
	}

	if usage.GetInputTokens() != 412 || usage.GetOutputTokens() != 28 {
		t.Errorf("usage = %+v, want input=412 output=28", usage)
	}
	if usage.ReasoningTokens != nil {
		t.Errorf("usage.ReasoningTokens = %v, want nil (never reported for this stream)", *usage.ReasoningTokens)
	}
	if stop != modelv1.StopReason_STOP_REASON_TOOL_USE {
		t.Errorf("stop = %v, want STOP_REASON_TOOL_USE", stop)
	}
}

// TestRefusalReasoningTokensNeverFolded transcribes examples.md's second
// sequence: a refusal whose usage reports reasoning_tokens distinctly from
// output_tokens.
func TestRefusalReasoningTokensNeverFolded(t *testing.T) {
	t.Parallel()

	evs := []*modelv1.StreamEvent{
		textDelta("I won't do that — it looks destructive and unconfirmed."),
		usageEvent(&modelv1.Usage{InputTokens: 201, OutputTokens: 19, ReasoningTokens: int64ptr(143)}),
		stopEvent(modelv1.StopReason_STOP_REASON_REFUSAL, ""),
	}
	a := observeAll(t, evs)

	msg, usage, stop, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if len(msg.GetContent()) != 1 || msg.GetContent()[0].GetText() == nil {
		t.Fatalf("Content = %+v, want a single TextBlock", msg.GetContent())
	}
	if usage.OutputTokens != 19 {
		t.Errorf("OutputTokens = %d, want 19 (never folding reasoning_tokens in)", usage.OutputTokens)
	}
	if usage.ReasoningTokens == nil || *usage.ReasoningTokens != 143 {
		t.Errorf("ReasoningTokens = %v, want 143", usage.ReasoningTokens)
	}
	if stop != modelv1.StopReason_STOP_REASON_REFUSAL {
		t.Errorf("stop = %v, want STOP_REASON_REFUSAL", stop)
	}
}

// TestReasoningTokensAbsentStaysNil asserts the accumulator never derives
// or zero-fills usage.reasoning_tokens when the vendor never reported it —
// determinism.md's "the fallback heuristic" spirit applied to this field:
// exactly one source of truth, never a synthesized second one.
func TestReasoningTokensAbsentStaysNil(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("42"),
		usageEvent(&modelv1.Usage{InputTokens: 10, OutputTokens: 1}),
		stopEvent(modelv1.StopReason_STOP_REASON_STOP_SEQUENCE, "</answer>"),
	})
	_, usage, stop, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if usage.ReasoningTokens != nil {
		t.Errorf("ReasoningTokens = %v, want nil", *usage.ReasoningTokens)
	}
	if stop != modelv1.StopReason_STOP_REASON_STOP_SEQUENCE {
		t.Errorf("stop = %v, want STOP_REASON_STOP_SEQUENCE", stop)
	}
}

// TestInterleavedToolCalls covers two tool_use blocks whose delta fragments
// interleave in the stream rather than running sequentially — both must
// accumulate correctly and independently, and both blocks must appear in
// the order their ToolCallStart events fired, not fragment-arrival order.
func TestInterleavedToolCalls(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		toolCallStart("tc_a", "alpha"),
		toolCallStart("tc_b", "beta"),
		toolCallDelta("tc_a", `{"x":`),
		toolCallDelta("tc_b", `{"y":`),
		toolCallDelta("tc_a", `1}`),
		toolCallDelta("tc_b", `2}`),
		toolCallDone("tc_b"),
		toolCallDone("tc_a"),
		stopEvent(modelv1.StopReason_STOP_REASON_TOOL_USE, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if len(msg.GetContent()) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(msg.GetContent()))
	}

	first := msg.GetContent()[0].GetToolUse()
	second := msg.GetContent()[1].GetToolUse()
	if first == nil || second == nil {
		t.Fatalf("Content = %+v, want two ToolUseBlocks", msg.GetContent())
	}
	if first.GetId() != "tc_a" {
		t.Errorf("Content[0].Id = %q, want tc_a (declared-order, not done-order)", first.GetId())
	}
	if second.GetId() != "tc_b" {
		t.Errorf("Content[1].Id = %q, want tc_b", second.GetId())
	}
	if diff := structDiff(first.GetArguments(), mustStruct(t, map[string]any{"x": float64(1)})); diff != "" {
		t.Errorf("tc_a arguments mismatch: %s", diff)
	}
	if diff := structDiff(second.GetArguments(), mustStruct(t, map[string]any{"y": float64(2)})); diff != "" {
		t.Errorf("tc_b arguments mismatch: %s", diff)
	}
}

// TestThinkingBlockSignatureAcrossMultipleEvents covers a thinking block
// whose signature arrives via more than one ThinkingSignature event before
// the block closes, asserting the bytes are concatenated and attached once
// — at the block's terminal point, here the following ToolCallStart.
func TestThinkingBlockSignatureAcrossMultipleEvents(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		thinkingDelta("Let me think "),
		thinkingDelta("about this."),
		thinkingSignature([]byte{0xDE, 0xAD}),
		thinkingSignature([]byte{0xBE, 0xEF}),
		toolCallStart("tc_1", "answer"),
		toolCallDone("tc_1"),
		stopEvent(modelv1.StopReason_STOP_REASON_TOOL_USE, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if len(msg.GetContent()) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(msg.GetContent()))
	}
	thinking := msg.GetContent()[0].GetThinking()
	if thinking == nil {
		t.Fatalf("Content[0] is not a ThinkingBlock: %+v", msg.GetContent()[0])
	}
	if want := "Let me think about this."; thinking.GetText() != want {
		t.Errorf("Thinking.Text = %q, want %q", thinking.GetText(), want)
	}
	wantSig := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if string(thinking.GetSignature()) != string(wantSig) {
		t.Errorf("Thinking.Signature = %x, want %x", thinking.GetSignature(), wantSig)
	}
}

// TestThinkingSignatureAttachedAtStreamEnd covers a thinking block that is
// still open when the stream terminates — closeOpenBlock must run on the
// terminal event too, not only when a subsequent block opens.
func TestThinkingSignatureAttachedAtStreamEnd(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		thinkingDelta("reasoning"),
		thinkingSignature([]byte("sig")),
		textDelta("answer"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	thinking := msg.GetContent()[0].GetThinking()
	if thinking == nil {
		t.Fatalf("Content[0] is not a ThinkingBlock: %+v", msg.GetContent()[0])
	}
	if string(thinking.GetSignature()) != "sig" {
		t.Errorf("Signature = %q, want %q", thinking.GetSignature(), "sig")
	}
}

// TestRedactedThinkingWholeBlock covers the whole-block, never-fragmented
// redacted_thinking variant — a single Observe call must produce a
// complete RedactedThinkingBlock, and it must sit correctly ordered
// between neighboring text blocks.
func TestRedactedThinkingWholeBlock(t *testing.T) {
	t.Parallel()

	opaque := []byte{0x01, 0x02, 0x03, 0xFF}
	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("before"),
		redactedThinking(opaque),
		textDelta("after"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if len(msg.GetContent()) != 3 {
		t.Fatalf("len(Content) = %d, want 3", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetText().GetText(); got != "before" {
		t.Errorf("Content[0].Text = %q, want before", got)
	}
	redacted := msg.GetContent()[1].GetRedactedThinking()
	if redacted == nil {
		t.Fatalf("Content[1] is not a RedactedThinkingBlock: %+v", msg.GetContent()[1])
	}
	if string(redacted.GetData()) != string(opaque) {
		t.Errorf("RedactedThinking.Data = %x, want %x", redacted.GetData(), opaque)
	}
	if got := msg.GetContent()[2].GetText().GetText(); got != "after" {
		t.Errorf("Content[2].Text = %q, want after", got)
	}
}

// TestContentBlockOrderingExplicit builds a sequence deliberately shaped so
// that an implementation relying on incidental map iteration or insertion
// order elsewhere would still pass by accident — this asserts the exact
// index of every block by type, not just aggregate counts.
func TestContentBlockOrderingExplicit(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("one"),
		toolCallStart("tc_1", "first"),
		toolCallDone("tc_1"),
		textDelta("two"),
		thinkingDelta("reasoning"),
		toolCallStart("tc_2", "second"),
		toolCallDone("tc_2"),
		stopEvent(modelv1.StopReason_STOP_REASON_TOOL_USE, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	content := msg.GetContent()
	if len(content) != 5 {
		t.Fatalf("len(Content) = %d, want 5", len(content))
	}
	checks := []struct {
		idx  int
		desc string
		ok   bool
	}{
		{0, "text", content[0].GetText() != nil},
		{1, "tool_use tc_1", content[1].GetToolUse().GetId() == "tc_1"},
		{2, "text", content[2].GetText() != nil},
		{3, "thinking", content[3].GetThinking() != nil},
		{4, "tool_use tc_2", content[4].GetToolUse().GetId() == "tc_2"},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("Content[%d] expected %s, got %+v", c.idx, c.desc, content[c.idx])
		}
	}
}

// TestNoArgumentToolCall covers a tool call whose ToolCallDone fires with
// no preceding ToolCallDelta fragments at all — Arguments must stay nil,
// not an empty-but-non-nil Struct.
func TestNoArgumentToolCall(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		toolCallStart("tc_1", "ping"),
		toolCallDone("tc_1"),
		stopEvent(modelv1.StopReason_STOP_REASON_TOOL_USE, ""),
	})
	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if args := msg.GetContent()[0].GetToolUse().GetArguments(); args != nil {
		t.Errorf("Arguments = %v, want nil", args)
	}
}

// TestErrTerminatedStream covers an Error-terminated stream: Err() must
// carry the ModelError and Result()'s StopReason stays unspecified, since
// no Stop event ever fired.
func TestErrTerminatedStream(t *testing.T) {
	t.Parallel()

	modelErr := &modelv1.ModelError{
		Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED,
		Message:  "vendor overloaded",
	}
	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("partial"),
		errorEvent(modelErr),
	})

	msg, _, stop, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if stop != modelv1.StopReason_STOP_REASON_UNSPECIFIED {
		t.Errorf("stop = %v, want STOP_REASON_UNSPECIFIED (no Stop event fired)", stop)
	}
	if len(msg.GetContent()) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(msg.GetContent()))
	}
	got := a.Err()
	if got == nil {
		t.Fatalf("Err() = nil, want the terminal ModelError")
	}
	if got.GetCategory() != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED || got.GetMessage() != "vendor overloaded" {
		t.Errorf("Err() = %+v, want category=OVERLOADED message=%q", got, "vendor overloaded")
	}
}

// TestErrNilForStopTerminatedStream asserts Err() is nil when the stream
// ended in an ordinary Stop, contrasting TestErrTerminatedStream.
func TestErrNilForStopTerminatedStream(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("done"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})
	if err := a.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

// TestResultNotOKMidStream asserts Result reports ok=false before any
// terminal event has been observed.
func TestResultNotOKMidStream(t *testing.T) {
	t.Parallel()

	a := New()
	if err := a.Observe(textDelta("still going")); err != nil {
		t.Fatalf("Observe() = %v, want nil", err)
	}
	msg, usage, stop, ok := a.Result()
	if ok {
		t.Fatalf("Result() ok = true, want false mid-stream")
	}
	if msg != nil || usage != nil || stop != modelv1.StopReason_STOP_REASON_UNSPECIFIED {
		t.Errorf("Result() = (%v, %v, %v, false), want all zero values", msg, usage, stop)
	}
}

// TestMalformedSequences covers every structurally-invalid StreamEvent
// sequence Observe must reject.
func TestMalformedSequences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		evs     []*modelv1.StreamEvent
		wantErr error // checked via errors.Is when non-nil
	}{
		{
			name:    "delta references unstarted tool call",
			evs:     []*modelv1.StreamEvent{toolCallDelta("ghost", "{}")},
			wantErr: ErrUnknownToolCall,
		},
		{
			name:    "done references unstarted tool call",
			evs:     []*modelv1.StreamEvent{toolCallDone("ghost")},
			wantErr: ErrUnknownToolCall,
		},
		{
			name: "duplicate tool call start",
			evs: []*modelv1.StreamEvent{
				toolCallStart("tc_1", "a"),
				toolCallStart("tc_1", "a"),
			},
			wantErr: ErrDuplicateToolCall,
		},
		{
			name: "delta after tool call done",
			evs: []*modelv1.StreamEvent{
				toolCallStart("tc_1", "a"),
				toolCallDone("tc_1"),
				toolCallDelta("tc_1", "{}"),
			},
			wantErr: ErrToolCallAlreadyDone,
		},
		{
			name: "double tool call done",
			evs: []*modelv1.StreamEvent{
				toolCallStart("tc_1", "a"),
				toolCallDone("tc_1"),
				toolCallDone("tc_1"),
			},
			wantErr: ErrToolCallAlreadyDone,
		},
		{
			name:    "thinking signature with no open thinking block",
			evs:     []*modelv1.StreamEvent{thinkingSignature([]byte("sig"))},
			wantErr: ErrThinkingSignatureWithoutBlock,
		},
		{
			name: "thinking signature after block closed by intervening event",
			evs: []*modelv1.StreamEvent{
				thinkingDelta("reasoning"),
				textDelta("switched to text"),
				thinkingSignature([]byte("sig")),
			},
			wantErr: ErrThinkingSignatureWithoutBlock,
		},
		{
			name: "two terminal events (stop then stop)",
			evs: []*modelv1.StreamEvent{
				stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
				stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
			},
			wantErr: ErrStreamTerminated,
		},
		{
			name: "two terminal events (stop then error)",
			evs: []*modelv1.StreamEvent{
				stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
				errorEvent(&modelv1.ModelError{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN}),
			},
			wantErr: ErrStreamTerminated,
		},
		{
			name: "event after error terminal",
			evs: []*modelv1.StreamEvent{
				errorEvent(&modelv1.ModelError{Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN}),
				textDelta("too late"),
			},
			wantErr: ErrStreamTerminated,
		},
		{
			name:    "nil event",
			evs:     []*modelv1.StreamEvent{nil},
			wantErr: ErrEmptyEvent,
		},
		{
			name:    "event with no oneof variant set",
			evs:     []*modelv1.StreamEvent{{}},
			wantErr: ErrEmptyEvent,
		},
		{
			name: "malformed tool call argument JSON",
			evs: []*modelv1.StreamEvent{
				toolCallStart("tc_1", "a"),
				toolCallDelta("tc_1", "{not valid json"),
				toolCallDone("tc_1"),
			},
			wantErr: nil, // asserted separately below: a wrapped json error, no fixed sentinel
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := New()
			var lastErr error
			for _, ev := range tt.evs {
				lastErr = a.Observe(ev)
				if lastErr != nil {
					break
				}
			}
			if lastErr == nil {
				t.Fatalf("Observe() = nil, want an error")
			}
			if tt.wantErr != nil && !errors.Is(lastErr, tt.wantErr) {
				t.Errorf("Observe() = %v, want errors.Is(_, %v)", lastErr, tt.wantErr)
			}
		})
	}
}

// TestObserveAfterErrorReturnsErrEvenForValidLookingEvent asserts that once
// Observe has returned a structural error, the accumulator does not
// silently keep accepting events for that same tool id afterward — a
// direct regression guard for the "delta after done" case beyond the
// table above, verifying the state truly stopped advancing.
func TestObserveAfterErrorReturnsErrEvenForValidLookingEvent(t *testing.T) {
	t.Parallel()

	a := New()
	mustObserve(t, a, toolCallStart("tc_1", "a"))
	mustObserve(t, a, toolCallDone("tc_1"))

	if err := a.Observe(toolCallDelta("tc_1", "{}")); !errors.Is(err, ErrToolCallAlreadyDone) {
		t.Fatalf("Observe() = %v, want ErrToolCallAlreadyDone", err)
	}
	// The tool call's arguments must still reflect the pre-error state
	// (no arguments) rather than having been mutated by the rejected call.
	mustObserve(t, a, stopEvent(modelv1.StopReason_STOP_REASON_TOOL_USE, ""))
	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatalf("Result() ok = false, want true")
	}
	if args := msg.GetContent()[0].GetToolUse().GetArguments(); args != nil {
		t.Errorf("Arguments = %v, want nil (rejected delta must not mutate state)", args)
	}
}

func mustObserve(t *testing.T, a *Accumulator, ev *modelv1.StreamEvent) {
	t.Helper()
	if err := a.Observe(ev); err != nil {
		t.Fatalf("Observe() = %v, want nil", err)
	}
}

// structDiff reports a human-readable difference between two
// *structpb.Struct values via their canonical JSON encoding, or "" if
// equal. Test-only: the package itself never needs to compare Structs.
func structDiff(got, want *structpb.Struct) string {
	gotJSON, _ := json.Marshal(got.AsMap())
	wantJSON, _ := json.Marshal(want.AsMap())
	if string(gotJSON) != string(wantJSON) {
		return "got " + string(gotJSON) + ", want " + string(wantJSON)
	}
	return ""
}

// TestStreamStart_recordedWithoutAffectingContent pins that a real
// provider's opening event is accepted rather than rejected as an
// unhandled variant, and that it contributes nothing to the message.
func TestStreamStart_recordedWithoutAffectingContent(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		streamStart("req-abc123"),
		textDelta("Hello World"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	if got := a.ProviderRequestID(); got != "req-abc123" {
		t.Errorf("ProviderRequestID = %q, want req-abc123", got)
	}
	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatal("Result reported ok = false after a Stop event")
	}
	if len(msg.GetContent()) != 1 {
		t.Fatalf("Content has %d blocks, want 1 — StreamStart must not create one", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetText().GetText(); got != "Hello World" {
		t.Errorf("text = %q, want Hello World", got)
	}
}

// TestStreamStart_isNotABlockBoundary asserts a StreamStart arriving
// between two text deltas leaves the open block open. It carries no
// content, so splitting on it would fabricate a second block.
func TestStreamStart_isNotABlockBoundary(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("Hello "),
		streamStart("req-late"),
		textDelta("World"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatal("Result reported ok = false after a Stop event")
	}
	if len(msg.GetContent()) != 1 {
		t.Fatalf("Content has %d blocks, want 1 unsplit block", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetText().GetText(); got != "Hello World" {
		t.Errorf("text = %q, want Hello World", got)
	}
}

// TestStreamStart_absentLeavesTheIDEmpty covers events.proto's MAY-omit:
// a provider that publishes no request id is not an error.
func TestStreamStart_absentLeavesTheIDEmpty(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("hi"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})
	if got := a.ProviderRequestID(); got != "" {
		t.Errorf("ProviderRequestID = %q, want empty when no StreamStart was observed", got)
	}
}

// metaEvent wraps a StreamMetadata into a StreamEvent.
func metaEvent(m *modelv1.StreamEvent_StreamMetadata) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_Metadata{Metadata: m}}
}

// TestStreamMetadata_isNotABlockBoundary is the property events.proto
// states explicitly: metadata may arrive mid-stream, so splitting a text
// run on it would corrupt any completion whose vendor revised its
// headers partway through.
func TestStreamMetadata_isNotABlockBoundary(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("Hello "),
		metaEvent(&modelv1.StreamEvent_StreamMetadata{ActualModel: proto.String("grok-4.3")}),
		textDelta("World"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatal("Result reported ok = false after a Stop event")
	}
	if len(msg.GetContent()) != 1 {
		t.Fatalf("Content has %d blocks, want 1 unsplit block", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetText().GetText(); got != "Hello World" {
		t.Errorf("text = %q, want Hello World", got)
	}
	if got := a.Metadata().GetActualModel(); got != "grok-4.3" {
		t.Errorf("ActualModel = %q, want grok-4.3", got)
	}
}

// TestStreamMetadata_mergesFieldByField pins the supersede rule: a later
// event overwrites the fields it sets and leaves the rest alone. Keeping
// only the newest event instead would drop an actual_model reported once
// at the top of a stream.
func TestStreamMetadata_mergesFieldByField(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		metaEvent(&modelv1.StreamEvent_StreamMetadata{
			ActualModel:       proto.String("grok-4.3"),
			SystemFingerprint: proto.String("fp_1"),
			Attrs:             map[string]string{"a": "1"},
		}),
		metaEvent(&modelv1.StreamEvent_StreamMetadata{
			SystemFingerprint: proto.String("fp_2"),
			ServiceTier:       proto.String("fast"),
			Attrs:             map[string]string{"b": "2"},
		}),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	m := a.Metadata()
	if got := m.GetActualModel(); got != "grok-4.3" {
		t.Errorf("ActualModel = %q, want grok-4.3 carried forward from the first event", got)
	}
	if got := m.GetSystemFingerprint(); got != "fp_2" {
		t.Errorf("SystemFingerprint = %q, want fp_2 (later event supersedes)", got)
	}
	if got := m.GetServiceTier(); got != "fast" {
		t.Errorf("ServiceTier = %q, want fast", got)
	}
	if got := m.GetAttrs(); got["a"] != "1" || got["b"] != "2" {
		t.Errorf("Attrs = %v, want both keys merged", got)
	}
}

// TestStreamMetadata_rateLimitsReplaceWholesale asserts rate_limits is
// snapshot-replaced rather than merged entry by entry. Each event
// carries the vendor's complete budget picture, so merging would
// resurrect a budget the vendor stopped reporting.
func TestStreamMetadata_rateLimitsReplaceWholesale(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		metaEvent(&modelv1.StreamEvent_StreamMetadata{RateLimits: []*modelv1.RateLimitSnapshot{
			{Kind: modelv1.RateLimitKind_RATE_LIMIT_KIND_REQUESTS},
			{Kind: modelv1.RateLimitKind_RATE_LIMIT_KIND_TOKENS},
		}}),
		metaEvent(&modelv1.StreamEvent_StreamMetadata{RateLimits: []*modelv1.RateLimitSnapshot{
			{Kind: modelv1.RateLimitKind_RATE_LIMIT_KIND_CREDITS},
		}}),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	got := a.Metadata().GetRateLimits()
	if len(got) != 1 {
		t.Fatalf("RateLimits has %d entries, want 1 — the later snapshot replaces, not merges", len(got))
	}
	if got[0].GetKind() != modelv1.RateLimitKind_RATE_LIMIT_KIND_CREDITS {
		t.Errorf("Kind = %v, want CREDITS", got[0].GetKind())
	}
}

// TestStreamMetadata_absentMeansNoNewInformation covers the one reading
// that would lose data: an event that sets nothing must not blank what
// an earlier event established.
func TestStreamMetadata_absentMeansNoNewInformation(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		metaEvent(&modelv1.StreamEvent_StreamMetadata{ActualModel: proto.String("grok-4.3")}),
		metaEvent(&modelv1.StreamEvent_StreamMetadata{}),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	if got := a.Metadata().GetActualModel(); got != "grok-4.3" {
		t.Errorf("ActualModel = %q, want it preserved across an empty metadata event", got)
	}
}

// TestStreamMetadata_absentEntirelyIsNil keeps the no-metadata case
// distinguishable from an empty one, so a caller can tell "the vendor
// said nothing" from "the vendor said nothing new".
func TestStreamMetadata_absentEntirelyIsNil(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("hi"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})
	if a.Metadata() != nil {
		t.Errorf("Metadata = %v, want nil when no metadata event was observed", a.Metadata())
	}
}

// TestStreamStart_correlationIDsAreCopied checks both that the extra
// vendor handles survive and that the accumulator does not hand out a
// map a caller could mutate underneath it.
func TestStreamStart_correlationIDsAreCopied(t *testing.T) {
	t.Parallel()

	ev := &modelv1.StreamEvent{Event: &modelv1.StreamEvent_StreamStart_{
		StreamStart: &modelv1.StreamEvent_StreamStart{
			ProviderRequestId: "req-1",
			CorrelationIds:    map[string]string{"cf-ray": "abc", "response_id": "resp_9"},
		},
	}}
	a := observeAll(t, []*modelv1.StreamEvent{ev, stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, "")})

	got := a.CorrelationIDs()
	if got["cf-ray"] != "abc" || got["response_id"] != "resp_9" {
		t.Fatalf("CorrelationIDs = %v, want both handles", got)
	}
	got["cf-ray"] = "mutated"
	if a.CorrelationIDs()["cf-ray"] != "abc" {
		t.Error("mutating the returned map changed the accumulator's own state")
	}
}

// thinkingOn builds a channel-tagged thinking delta.
func thinkingOn(text string, ch modelv1.StreamEvent_ThinkingChannel) *modelv1.StreamEvent {
	return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_ThinkingDelta_{
		ThinkingDelta: &modelv1.StreamEvent_ThinkingDelta{Text: text, Channel: ch},
	}}
}

// TestThinkingChannel_switchClosesTheBlock asserts a summary run and a
// raw-reasoning run stay separate blocks even when adjacent. Merging them
// on adjacency alone would produce one block that reads as neither.
func TestThinkingChannel_switchClosesTheBlock(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		thinkingOn("raw ", modelv1.StreamEvent_THINKING_CHANNEL_CONTENT),
		thinkingOn("reasoning", modelv1.StreamEvent_THINKING_CHANNEL_CONTENT),
		thinkingOn("a summary", modelv1.StreamEvent_THINKING_CHANNEL_SUMMARY),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, ok := a.Result()
	if !ok {
		t.Fatal("Result reported ok = false after a Stop event")
	}
	if len(msg.GetContent()) != 2 {
		t.Fatalf("Content has %d blocks, want 2 (one per channel)", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetThinking().GetText(); got != "raw reasoning" {
		t.Errorf("block 0 = %q, want the coalesced content run", got)
	}
	if got := msg.GetContent()[1].GetThinking().GetText(); got != "a summary" {
		t.Errorf("block 1 = %q, want the summary run", got)
	}
}

// TestThinkingChannel_unspecifiedStillCoalesces is the compatibility
// guarantee: a provider that sets no channel behaves exactly as before
// this field existed.
func TestThinkingChannel_unspecifiedStillCoalesces(t *testing.T) {
	t.Parallel()

	a := observeAll(t, []*modelv1.StreamEvent{
		thinkingDelta("one "),
		thinkingDelta("block"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, _ := a.Result()
	if len(msg.GetContent()) != 1 {
		t.Fatalf("Content has %d blocks, want 1 unsplit block", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetThinking().GetText(); got != "one block" {
		t.Errorf("text = %q, want %q", got, "one block")
	}
}

// TestSafetyNotice_recordedInOrderAndNotABlockBoundary covers both
// properties at once: the sequence is the explanation an operator needs,
// and interposing on a text run must not split it.
func TestSafetyNotice_recordedInOrderAndNotABlockBoundary(t *testing.T) {
	t.Parallel()

	notice := func(k modelv1.StreamEvent_SafetyKind) *modelv1.StreamEvent {
		return &modelv1.StreamEvent{Event: &modelv1.StreamEvent_SafetyNotice_{
			SafetyNotice: &modelv1.StreamEvent_SafetyNotice{Kind: k},
		}}
	}
	a := observeAll(t, []*modelv1.StreamEvent{
		textDelta("Hello "),
		notice(modelv1.StreamEvent_SAFETY_KIND_BUFFERING),
		notice(modelv1.StreamEvent_SAFETY_KIND_MODERATION),
		textDelta("World"),
		stopEvent(modelv1.StopReason_STOP_REASON_END_TURN, ""),
	})

	msg, _, _, _ := a.Result()
	if len(msg.GetContent()) != 1 {
		t.Fatalf("Content has %d blocks, want 1 unsplit block", len(msg.GetContent()))
	}
	if got := msg.GetContent()[0].GetText().GetText(); got != "Hello World" {
		t.Errorf("text = %q, want Hello World", got)
	}

	got := a.SafetyNotices()
	if len(got) != 2 {
		t.Fatalf("SafetyNotices has %d entries, want 2", len(got))
	}
	if got[0].GetKind() != modelv1.StreamEvent_SAFETY_KIND_BUFFERING ||
		got[1].GetKind() != modelv1.StreamEvent_SAFETY_KIND_MODERATION {
		t.Errorf("notices = %v/%v, want BUFFERING then MODERATION in arrival order",
			got[0].GetKind(), got[1].GetKind())
	}
}
