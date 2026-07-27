package messages

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// sinkCall is one recorded EventSink method invocation, used to assert
// exact call sequences against a fakeSink.
type sinkCall struct {
	method string
	args   []any
}

// fakeSink is a hand-written recording EventSink, per .claude/rules/go-testing.md
// — no mocking framework, an in-memory fake implementing the interface
// directly. failAt lets a test inject an error return from one named
// method, exactly once semantics not required since every test either
// fails fast or doesn't touch that method again.
type fakeSink struct {
	calls  []sinkCall
	failAt map[string]error
	// before, when set for a method name, runs immediately before that
	// call is recorded — client_test.go's cancellation test uses this to
	// cancel the real context from inside a sink call, reproducing how
	// pkg/model's Sink detects a kernel-side stream close mid-send.
	before map[string]func()
}

func newFakeSink() *fakeSink {
	return &fakeSink{failAt: map[string]error{}, before: map[string]func(){}}
}

func (f *fakeSink) record(method string, args ...any) error {
	if hook := f.before[method]; hook != nil {
		hook()
	}
	f.calls = append(f.calls, sinkCall{method: method, args: args})
	return f.failAt[method]
}

func (f *fakeSink) StreamStart(providerRequestID string) error {
	return f.record("StreamStart", providerRequestID)
}

func (f *fakeSink) TextDelta(text string) error { return f.record("TextDelta", text) }

func (f *fakeSink) ThinkingDelta(text string) error { return f.record("ThinkingDelta", text) }

func (f *fakeSink) ThinkingSignature(signature []byte) error {
	return f.record("ThinkingSignature", string(signature))
}

func (f *fakeSink) RedactedThinking(data []byte) error {
	return f.record("RedactedThinking", string(data))
}

func (f *fakeSink) ToolCallStart(id, name string) error {
	return f.record("ToolCallStart", id, name)
}

func (f *fakeSink) ToolCallDelta(id, argumentsFragment string) error {
	return f.record("ToolCallDelta", id, argumentsFragment)
}

func (f *fakeSink) ToolCallDone(id string) error { return f.record("ToolCallDone", id) }

func (f *fakeSink) Usage(u model.Usage) error { return f.record("Usage", u) }

func (f *fakeSink) Stop(reason modelv1.StopReason, matchedStopSequence string) error {
	return f.record("Stop", reason, matchedStopSequence)
}

func (f *fakeSink) Error(modelErr *model.Error) error { return f.record("Error", modelErr) }

var _ EventSink = (*fakeSink)(nil)

func i64p(v int64) *int64 { return &v }

func assertCalls(t *testing.T, got []sinkCall, want []sinkCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i].method != want[i].method || !reflect.DeepEqual(got[i].args, want[i].args) {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// handleAll feeds every event in order into tr, failing the test on the
// first error and returning whether a terminal event was emitted.
func handleAll(t *testing.T, tr *Translator, events []StreamEvent) bool {
	t.Helper()
	done := false
	for _, ev := range events {
		var err error
		done, err = tr.Handle(ev)
		if err != nil {
			t.Fatalf("Handle(%q): %v", ev.Type, err)
		}
	}
	return done
}

// TestTranslator_workedSequence reproduces
// docs/specifications/model/examples.md's full StreamCompletion event
// sequence: text, then one tool call, then usage and a tool_use stop.
func TestTranslator_workedSequence(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)

	events := []StreamEvent{
		{Type: eventMessageStart, Message: &StreamMessage{Usage: &Usage{InputTokens: i64p(412)}}},
		{Type: eventContentBlockStart, Index: 0, ContentBlock: &Block{Type: blockText}},
		{Type: eventContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: deltaText, Text: "Let me check "}},
		{Type: eventContentBlockDelta, Index: 0, Delta: &StreamDelta{Type: deltaText, Text: "that file."}},
		{Type: eventContentBlockStop, Index: 0},
		{Type: eventContentBlockStart, Index: 1, ContentBlock: &Block{Type: blockToolUse, ID: "tc_1", Name: "read_file"}},
		{Type: eventContentBlockDelta, Index: 1, Delta: &StreamDelta{Type: deltaInputJSON, PartialJSON: `{"path":`}},
		{Type: eventContentBlockDelta, Index: 1, Delta: &StreamDelta{Type: deltaInputJSON, PartialJSON: `"main.go"}`}},
		{Type: eventContentBlockStop, Index: 1},
		{Type: eventMessageDelta, Usage: &Usage{OutputTokens: i64p(28)}, Delta: &StreamDelta{StopReason: stopToolUse}},
		{Type: eventMessageStop},
	}

	done := handleAll(t, tr, events)
	if !done {
		t.Fatalf("done = false after message_stop, want true")
	}

	want := []sinkCall{
		{method: "TextDelta", args: []any{"Let me check "}},
		{method: "TextDelta", args: []any{"that file."}},
		{method: "ToolCallStart", args: []any{"tc_1", "read_file"}},
		{method: "ToolCallDelta", args: []any{"tc_1", `{"path":`}},
		{method: "ToolCallDelta", args: []any{"tc_1", `"main.go"}`}},
		{method: "ToolCallDone", args: []any{"tc_1"}},
		{method: "Usage", args: []any{model.Usage{InputTokens: 412, OutputTokens: 28}}},
		{method: "Stop", args: []any{modelv1.StopReason_STOP_REASON_TOOL_USE, ""}},
	}
	assertCalls(t, sink.calls, want)
}

func TestTranslator_contentBlockStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		block *Block
		want  []sinkCall
	}{
		{
			name:  "text needs no action",
			block: &Block{Type: blockText},
			want:  nil,
		},
		{
			name:  "thinking needs no action",
			block: &Block{Type: blockThinking},
			want:  nil,
		},
		{
			name:  "tool_use starts a tool call",
			block: &Block{Type: blockToolUse, ID: "tc_1", Name: "read_file"},
			want:  []sinkCall{{method: "ToolCallStart", args: []any{"tc_1", "read_file"}}},
		},
		{
			name:  "redacted_thinking passes through untouched",
			block: &Block{Type: blockRedactedThinking, Data: "QUJDREVGRw=="},
			want:  []sinkCall{{method: "RedactedThinking", args: []any{"QUJDREVGRw=="}}},
		},
		{
			name:  "unrecognized block type is ignored",
			block: &Block{Type: "server_tool_use"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sink := newFakeSink()
			tr := NewTranslator(sink)
			done, err := tr.Handle(StreamEvent{Type: eventContentBlockStart, Index: 0, ContentBlock: tt.block})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if done {
				t.Fatalf("done = true, want false")
			}
			assertCalls(t, sink.calls, tt.want)
		})
	}
}

func TestTranslator_contentBlockStart_nilContentBlock(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	done, err := tr.Handle(StreamEvent{Type: eventContentBlockStart, Index: 0})
	if err != nil || done {
		t.Fatalf("Handle = (%v, %v), want (false, nil)", done, err)
	}
	assertCalls(t, sink.calls, nil)
}

func TestTranslator_redactedThinkingBytesPassThroughUndecoded(t *testing.T) {
	t.Parallel()

	// The literal ASCII bytes of the vendor's base64 text must arrive
	// exactly as sent — this must NOT be base64-decoded on the way
	// through (see this package's CLAUDE.md).
	const rawBase64 = "SGVsbG8sIHdvcmxkIQ=="
	sink := newFakeSink()
	tr := NewTranslator(sink)

	_, err := tr.Handle(StreamEvent{
		Type:  eventContentBlockStart,
		Index: 0,
		ContentBlock: &Block{
			Type: blockRedactedThinking,
			Data: rawBase64,
		},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCalls(t, sink.calls, []sinkCall{{method: "RedactedThinking", args: []any{rawBase64}}})
}

func TestTranslator_signatureBytesPassThroughUndecoded(t *testing.T) {
	t.Parallel()

	const rawBase64 = "c2lnbmF0dXJlLWJ5dGVz"
	sink := newFakeSink()
	tr := NewTranslator(sink)

	_, err := tr.Handle(StreamEvent{
		Type:  eventContentBlockDelta,
		Index: 0,
		Delta: &StreamDelta{Type: deltaSignature, Signature: rawBase64},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCalls(t, sink.calls, []sinkCall{{method: "ThinkingSignature", args: []any{rawBase64}}})
}

func TestTranslator_contentBlockDelta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		delta *StreamDelta
		want  []sinkCall
	}{
		{
			name:  "text_delta",
			delta: &StreamDelta{Type: deltaText, Text: "hi"},
			want:  []sinkCall{{method: "TextDelta", args: []any{"hi"}}},
		},
		{
			name:  "thinking_delta",
			delta: &StreamDelta{Type: deltaThinking, Thinking: "pondering"},
			want:  []sinkCall{{method: "ThinkingDelta", args: []any{"pondering"}}},
		},
		{
			name:  "unrecognized delta type is ignored",
			delta: &StreamDelta{Type: "some_future_delta"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sink := newFakeSink()
			tr := NewTranslator(sink)
			_, err := tr.Handle(StreamEvent{Type: eventContentBlockDelta, Index: 0, Delta: tt.delta})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			assertCalls(t, sink.calls, tt.want)
		})
	}
}

func TestTranslator_contentBlockDelta_nilDelta(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	_, err := tr.Handle(StreamEvent{Type: eventContentBlockDelta, Index: 0})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCalls(t, sink.calls, nil)
}

func TestTranslator_inputJSONDeltaWithoutMatchingToolUseIsIgnored(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	_, err := tr.Handle(StreamEvent{
		Type:  eventContentBlockDelta,
		Index: 7,
		Delta: &StreamDelta{Type: deltaInputJSON, PartialJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCalls(t, sink.calls, nil)
}

func TestTranslator_contentBlockStopWithoutMatchingToolUseIsIgnored(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	_, err := tr.Handle(StreamEvent{Type: eventContentBlockStop, Index: 3})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertCalls(t, sink.calls, nil)
}

func TestTranslator_stopReasonMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		vendorReason string
		want         modelv1.StopReason
	}{
		{"end_turn", stopEndTurn, modelv1.StopReason_STOP_REASON_END_TURN},
		{"tool_use", stopToolUse, modelv1.StopReason_STOP_REASON_TOOL_USE},
		{"max_tokens", stopMaxTokens, modelv1.StopReason_STOP_REASON_MAX_TOKENS},
		{"stop_sequence", stopStopSequence, modelv1.StopReason_STOP_REASON_STOP_SEQUENCE},
		{"refusal", stopRefusal, modelv1.StopReason_STOP_REASON_REFUSAL},
		{"pause_turn maps to end_turn", stopPauseTurn, modelv1.StopReason_STOP_REASON_END_TURN},
		{"unknown maps to end_turn", "some_future_reason", modelv1.StopReason_STOP_REASON_END_TURN},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sink := newFakeSink()
			tr := NewTranslator(sink)
			handleAll(t, tr, []StreamEvent{
				{Type: eventMessageDelta, Delta: &StreamDelta{StopReason: tt.vendorReason}},
				{Type: eventMessageStop},
			})
			assertCalls(t, sink.calls, []sinkCall{{method: "Stop", args: []any{tt.want, ""}}})
		})
	}
}

func TestTranslator_stopSequencePassedThrough(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	handleAll(t, tr, []StreamEvent{
		{Type: eventMessageDelta, Delta: &StreamDelta{StopReason: stopStopSequence, StopSequence: "</answer>"}},
		{Type: eventMessageStop},
	})
	assertCalls(t, sink.calls, []sinkCall{
		{method: "Stop", args: []any{modelv1.StopReason_STOP_REASON_STOP_SEQUENCE, "</answer>"}},
	})
}

func TestTranslator_defaultStopReasonWithoutMessageDelta(t *testing.T) {
	t.Parallel()

	// message_stop with no preceding message_delta is not expected by the
	// protocol, but must still report a defined reason.
	sink := newFakeSink()
	tr := NewTranslator(sink)
	handleAll(t, tr, []StreamEvent{{Type: eventMessageStop}})
	assertCalls(t, sink.calls, []sinkCall{
		{method: "Stop", args: []any{modelv1.StopReason_STOP_REASON_END_TURN, ""}},
	})
}

func TestTranslator_usageMergeAcrossMessageStartAndDelta(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	handleAll(t, tr, []StreamEvent{
		{Type: eventMessageStart, Message: &StreamMessage{Usage: &Usage{
			InputTokens:              i64p(100),
			CacheReadInputTokens:     i64p(10),
			CacheCreationInputTokens: i64p(20),
		}}},
		{Type: eventMessageDelta, Usage: &Usage{OutputTokens: i64p(50)}, Delta: &StreamDelta{StopReason: stopEndTurn}},
		{Type: eventMessageStop},
	})

	wantUsage := model.Usage{
		InputTokens:      100,
		OutputTokens:     50,
		CacheReadTokens:  i64p(10),
		CacheWriteTokens: i64p(20),
	}
	assertCalls(t, sink.calls, []sinkCall{
		{method: "Usage", args: []any{wantUsage}},
		{method: "Stop", args: []any{modelv1.StopReason_STOP_REASON_END_TURN, ""}},
	})
}

func TestTranslator_noUsageEventsMeansNoUsageCall(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	handleAll(t, tr, []StreamEvent{{Type: eventMessageStop}})
	assertCalls(t, sink.calls, []sinkCall{
		{method: "Stop", args: []any{modelv1.StopReason_STOP_REASON_END_TURN, ""}},
	})
}

func TestTranslator_reasoningTokensNeverSet(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	handleAll(t, tr, []StreamEvent{
		{Type: eventMessageStart, Message: &StreamMessage{Usage: &Usage{InputTokens: i64p(1)}}},
		{Type: eventMessageStop},
	})
	if len(sink.calls) == 0 || sink.calls[0].method != "Usage" {
		t.Fatalf("calls = %+v, want a Usage call first", sink.calls)
	}
	got := sink.calls[0].args[0].(model.Usage)
	if got.ReasoningTokens != nil {
		t.Fatalf("ReasoningTokens = %v, want nil", got.ReasoningTokens)
	}
}

func TestTranslator_midStreamError(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	done, err := tr.Handle(StreamEvent{
		Type:  eventError,
		Error: &APIErrorBody{Type: errOverloaded, Message: "vendor overloaded"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true")
	}
	if len(sink.calls) != 1 || sink.calls[0].method != "Error" {
		t.Fatalf("calls = %+v", sink.calls)
	}
	modelErr, ok := sink.calls[0].args[0].(*model.Error)
	if !ok || modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED || !modelErr.Retryable {
		t.Fatalf("classified error = %+v", modelErr)
	}
}

func TestTranslator_midStreamErrorWithNilBody(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	done, err := tr.Handle(StreamEvent{Type: eventError})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !done {
		t.Fatalf("done = false, want true")
	}
	modelErr, ok := sink.calls[0].args[0].(*model.Error)
	if !ok || modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN {
		t.Fatalf("classified error = %+v", modelErr)
	}
}

func TestTranslator_pingIsIgnored(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	done, err := tr.Handle(StreamEvent{Type: eventPing})
	if err != nil || done {
		t.Fatalf("Handle = (%v, %v), want (false, nil)", done, err)
	}
	assertCalls(t, sink.calls, nil)
}

func TestTranslator_unknownTopLevelEventIsIgnored(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	tr := NewTranslator(sink)
	done, err := tr.Handle(StreamEvent{Type: "some_future_event"})
	if err != nil || done {
		t.Fatalf("Handle = (%v, %v), want (false, nil)", done, err)
	}
	assertCalls(t, sink.calls, nil)
}

func TestTranslator_sinkErrorsPropagate(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("sink failure")

	tests := []struct {
		name  string
		setup func(*fakeSink)
		event StreamEvent
		done  bool
	}{
		{
			name:  "TextDelta failure",
			setup: func(f *fakeSink) { f.failAt["TextDelta"] = wantErr },
			event: StreamEvent{Type: eventContentBlockDelta, Delta: &StreamDelta{Type: deltaText, Text: "x"}},
			done:  false,
		},
		{
			name:  "ToolCallStart failure",
			setup: func(f *fakeSink) { f.failAt["ToolCallStart"] = wantErr },
			event: StreamEvent{Type: eventContentBlockStart, ContentBlock: &Block{Type: blockToolUse, ID: "t", Name: "n"}},
			done:  false,
		},
		{
			name:  "Usage failure at message_stop",
			setup: func(f *fakeSink) { f.failAt["Usage"] = wantErr },
			event: StreamEvent{Type: eventMessageStop},
			done:  true,
		},
		{
			name:  "Stop failure at message_stop",
			setup: func(f *fakeSink) { f.failAt["Stop"] = wantErr },
			event: StreamEvent{Type: eventMessageStop},
			done:  true,
		},
		{
			name:  "Error failure on mid-stream error event",
			setup: func(f *fakeSink) { f.failAt["Error"] = wantErr },
			event: StreamEvent{Type: eventError, Error: &APIErrorBody{Type: errAPI}},
			done:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sink := newFakeSink()
			tt.setup(sink)
			tr := NewTranslator(sink)

			if tt.name == "Usage failure at message_stop" {
				tr.usageSeen = true
			}

			done, err := tr.Handle(tt.event)
			if !errors.Is(err, wantErr) {
				t.Fatalf("err = %v, want %v", err, wantErr)
			}
			if done != tt.done {
				t.Fatalf("done = %v, want %v", done, tt.done)
			}
		})
	}
}
