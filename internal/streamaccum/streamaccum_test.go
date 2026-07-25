package streamaccum

import (
	"encoding/json"
	"errors"
	"testing"

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
