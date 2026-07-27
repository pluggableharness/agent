package messages

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	content "github.com/pluggableharness/agent/pkg/content"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	schemapkg "github.com/pluggableharness/agent/pkg/schema"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// fullSpec is a model.Spec with every content-block capability enabled and
// discrete-effort thinking, used as the default test fixture for
// capability-gated content blocks and effort-ladder thinking.
func fullSpec() model.Spec {
	return model.Spec{
		ID:                "claude-opus-5",
		MaxOutputTokens:   4096,
		SupportsToolUse:   true,
		SupportsVision:    true,
		SupportsDocuments: true,
		Thinking: model.ThinkingSpec{
			Supported: true,
			Effort: &model.EffortControl{
				Levels:  []string{"low", "medium", "high"},
				Default: "medium",
			},
			AdaptiveByDefault: true,
			Disable:           modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_NEVER,
		},
		Caching: model.CachingSpec{
			Supported:       true,
			ExplicitMarkers: true,
		},
	}
}

// budgetSpec is a model.Spec using continuous-budget thinking instead of
// discrete effort, used to exercise the budget-token path and the
// temperature-inclusion rule (only the effort ladder rejects temperature).
func budgetSpec(canDisable bool) model.Spec {
	disable := modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_NEVER
	if canDisable {
		disable = modelv1.ThinkingDisableSupport_THINKING_DISABLE_SUPPORT_ALWAYS
	}
	return model.Spec{
		ID:              "claude-legacy",
		MaxOutputTokens: 4096,
		SupportsToolUse: true,
		Thinking: model.ThinkingSpec{
			Supported: true,
			Budget: &model.BudgetControl{
				Range: model.ThinkingBudgetRange{Min: 1024, Max: 32000},
			},
			Disable: disable,
		},
		Caching: model.CachingSpec{},
	}
}

// minimalSpec has no optional content-block capability and no
// thinking/caching support, used to exercise every capability-gate
// rejection.
func minimalSpec() model.Spec {
	return model.Spec{
		ID:              "claude-minimal",
		MaxOutputTokens: 2048,
		Thinking:        model.ThinkingSpec{},
		Caching:         model.CachingSpec{},
	}
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}

// decodeJSON unmarshals raw into a generic map for structural comparison,
// the same technique schema_test.go uses.
func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, raw)
	}
	return m
}

// TestBuildRequest_fullWorkedExample builds the request from
// docs/specifications/model/examples.md#a-full-streamcompletion-event-sequence
// and asserts the resulting Request's shape: the assembled-context section
// wrapped into `system` with its cache breakpoint applied, the tool
// declaration translated through schemaToJSON, and the single user message.
func TestBuildRequest_fullWorkedExample(t *testing.T) {
	t.Parallel()

	pathSchema := schemapkg.String()
	inputSchema, err := schemapkg.Object(map[string]*schemav1.Schema{"path": pathSchema}, schemapkg.WithRequired("path"))
	if err != nil {
		t.Fatalf("build tool schema: %v", err)
	}
	wantToolSchema, err := schemaToJSON(inputSchema)
	if err != nil {
		t.Fatalf("schemaToJSON: %v", err)
	}

	in := &modelv1.StreamCompletionRequest{
		Messages: []*contentv1.Message{
			{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("What's in main.go?")}},
		},
		ModelId: "claude-opus-5",
		Tools: []*modelv1.ToolDeclaration{
			{Name: "read_file", InputSchema: inputSchema},
		},
		AssembledContext: []*contentv1.ContextSection{
			{
				Provider:  "project-context",
				Label:     "CLAUDE.md",
				Content:   []*contentv1.ContentBlock{content.Text("This is CLAUDE.md content.")},
				Tokens:    812,
				Stability: contentv1.Stability_STABILITY_STATIC,
			},
		},
		CacheBreakpoints: []*modelv1.CacheBreakpoint{
			{Position: &modelv1.CacheBreakpoint_AfterAssembledContext_{AfterAssembledContext: &modelv1.CacheBreakpoint_AfterAssembledContext{}}},
		},
	}

	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal Request: %v", err)
	}

	want := map[string]any{
		"model":      "claude-opus-5",
		"max_tokens": float64(4096),
		"stream":     true,
		"system": []any{
			map[string]any{
				"type":          "text",
				"text":          "<CLAUDE.md>\nThis is CLAUDE.md content.\n</CLAUDE.md>",
				"cache_control": map[string]any{"type": "ephemeral"},
			},
		},
		"tools": []any{
			map[string]any{
				"name":         "read_file",
				"input_schema": decodeJSON(t, wantToolSchema),
			},
		},
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "What's in main.go?"},
				},
			},
		},
	}

	if got := decodeJSON(t, raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v,\nwant %#v", got, want)
	}
}

// TestBuildRequest_messageCoalescing verifies that consecutive same-role
// messages merge into one, and specifically that several ToolResultBlocks
// answering one assistant turn — each arriving as its own canonical
// message, as the kernel does for parallel tool calls — land in a single
// user message.
func TestBuildRequest_messageCoalescing(t *testing.T) {
	t.Parallel()

	in := &modelv1.StreamCompletionRequest{
		ModelId: "claude-opus-5",
		Messages: []*contentv1.Message{
			{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("Hi")}},
			{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("there")}},
			{Role: contentv1.Role_ROLE_ASSISTANT, Content: []*contentv1.ContentBlock{
				content.ToolUse("tc1", "read_file", mustStruct(t, map[string]any{"path": "a.go"})),
			}},
			{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.ToolResult("tc1", content.Text("r1"))}},
			{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.ToolResult("tc2", content.Text("r2"))}},
		},
	}

	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.Messages) != 3 {
		t.Fatalf("expected 3 coalesced messages, got %d: %#v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != roleUser || len(got.Messages[0].Content) != 2 {
		t.Fatalf("message 0: expected 2 merged user blocks, got %#v", got.Messages[0])
	}
	if got.Messages[1].Role != roleAssistant || len(got.Messages[1].Content) != 1 {
		t.Fatalf("message 1: expected 1 assistant block, got %#v", got.Messages[1])
	}
	if got.Messages[2].Role != roleUser || len(got.Messages[2].Content) != 2 {
		t.Fatalf("message 2: expected 2 merged tool_result blocks, got %#v", got.Messages[2])
	}
	if got.Messages[2].Content[0].ToolUseID != "tc1" || got.Messages[2].Content[1].ToolUseID != "tc2" {
		t.Fatalf("message 2: tool_result ids not preserved in order: %#v", got.Messages[2].Content)
	}
}

func TestBuildRequest_toolChoiceModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		choice  *modelv1.ToolChoice
		want    *ToolChoice
		wantErr bool
	}{
		{name: "unset", choice: nil, want: nil},
		{name: "auto", choice: &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO}, want: &ToolChoice{Type: toolChoiceAuto}},
		{name: "any", choice: &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY}, want: &ToolChoice{Type: toolChoiceAny}},
		{name: "none", choice: &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_NONE}, want: &ToolChoice{Type: toolChoiceNone}},
		{
			name:   "specific with name",
			choice: &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC, ToolName: strptr("delete_repo")},
			want:   &ToolChoice{Type: toolChoiceTool, Name: "delete_repo"},
		},
		{
			name:    "specific without name is rejected",
			choice:  &modelv1.ToolChoice{Mode: modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &modelv1.StreamCompletionRequest{
				ModelId:  "m",
				Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}},
				Params:   &modelv1.GenerationParams{ToolChoice: tc.choice},
			}
			got, err := BuildRequest(in, fullSpec())
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got.ToolChoice, tc.want) {
				t.Fatalf("got %#v, want %#v", got.ToolChoice, tc.want)
			}
		})
	}
}

func strptr(s string) *string { return &s }

func TestBuildRequest_stopSequences(t *testing.T) {
	t.Parallel()

	in := &modelv1.StreamCompletionRequest{
		ModelId:  "m",
		Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}},
		Params:   &modelv1.GenerationParams{StopSequences: []string{"</answer>"}},
	}
	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got.StopSequences, []string{"</answer>"}) {
		t.Fatalf("got %#v", got.StopSequences)
	}
}

func TestBuildRequest_maxTokens(t *testing.T) {
	t.Parallel()

	msg := []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}}

	t.Run("falls back to spec default when unset", func(t *testing.T) {
		t.Parallel()
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.MaxTokens != fullSpec().MaxOutputTokens {
			t.Fatalf("got %d, want %d", got.MaxTokens, fullSpec().MaxOutputTokens)
		}
	})

	t.Run("falls back to spec default when zero", func(t *testing.T) {
		t.Parallel()
		zero := int64(0)
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{MaxOutputTokens: &zero}}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.MaxTokens != fullSpec().MaxOutputTokens {
			t.Fatalf("got %d, want %d", got.MaxTokens, fullSpec().MaxOutputTokens)
		}
	})

	t.Run("uses override when positive", func(t *testing.T) {
		t.Parallel()
		override := int64(8000)
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{MaxOutputTokens: &override}}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.MaxTokens != 8000 {
			t.Fatalf("got %d, want 8000", got.MaxTokens)
		}
	})
}

func TestBuildRequest_thinkingEffort(t *testing.T) {
	t.Parallel()

	msg := []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}}

	t.Run("valid effort level", func(t *testing.T) {
		t.Parallel()
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{ThinkingEffort: strptr("high")}}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Thinking == nil || got.Thinking.Type != thinkingTypeAdaptive {
			t.Fatalf("got Thinking %#v", got.Thinking)
		}
		if got.OutputConfig == nil || got.OutputConfig.Effort != "high" {
			t.Fatalf("got OutputConfig %#v", got.OutputConfig)
		}
	})

	t.Run("effort level outside model's ladder is rejected", func(t *testing.T) {
		t.Parallel()
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{ThinkingEffort: strptr("ultra")}}
		if _, err := BuildRequest(in, fullSpec()); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("no effort requested leaves both nil", func(t *testing.T) {
		t.Parallel()
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Thinking != nil || got.OutputConfig != nil {
			t.Fatalf("expected both nil, got Thinking=%#v OutputConfig=%#v", got.Thinking, got.OutputConfig)
		}
	})

	t.Run("temperature is dropped on the discrete-effort ladder", func(t *testing.T) {
		t.Parallel()
		temp := 0.7
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{Temperature: &temp}}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Temperature != nil {
			t.Fatalf("expected nil temperature, got %v", *got.Temperature)
		}
	})
}

func TestBuildRequest_thinkingBudget(t *testing.T) {
	t.Parallel()

	msg := []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}}

	t.Run("valid budget within range", func(t *testing.T) {
		t.Parallel()
		budget := int64(8000)
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{ThinkingBudgetTokens: &budget}}
		got, err := BuildRequest(in, budgetSpec(true))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Thinking == nil || got.Thinking.Type != thinkingTypeEnabled || got.Thinking.BudgetTokens == nil || *got.Thinking.BudgetTokens != 8000 {
			t.Fatalf("got Thinking %#v", got.Thinking)
		}
		if got.OutputConfig != nil {
			t.Fatalf("expected nil OutputConfig, got %#v", got.OutputConfig)
		}
	})

	t.Run("budget outside range is rejected", func(t *testing.T) {
		t.Parallel()
		budget := int64(100)
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{ThinkingBudgetTokens: &budget}}
		if _, err := BuildRequest(in, budgetSpec(true)); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("zero budget disables thinking when the model allows it", func(t *testing.T) {
		t.Parallel()
		zero := int64(0)
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{ThinkingBudgetTokens: &zero}}
		got, err := BuildRequest(in, budgetSpec(true))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Thinking == nil || got.Thinking.Type != thinkingTypeDisabled {
			t.Fatalf("got Thinking %#v", got.Thinking)
		}
	})

	t.Run("zero budget is rejected when the model cannot disable thinking", func(t *testing.T) {
		t.Parallel()
		zero := int64(0)
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{ThinkingBudgetTokens: &zero}}
		if _, err := BuildRequest(in, budgetSpec(false)); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("no budget requested leaves both nil", func(t *testing.T) {
		t.Parallel()
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg}
		got, err := BuildRequest(in, budgetSpec(true))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Thinking != nil || got.OutputConfig != nil {
			t.Fatalf("expected both nil, got Thinking=%#v OutputConfig=%#v", got.Thinking, got.OutputConfig)
		}
	})

	t.Run("temperature is kept on continuous-budget models", func(t *testing.T) {
		t.Parallel()
		temp := 0.5
		in := &modelv1.StreamCompletionRequest{ModelId: "m", Messages: msg, Params: &modelv1.GenerationParams{Temperature: &temp}}
		got, err := BuildRequest(in, budgetSpec(true))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Temperature == nil || *got.Temperature != 0.5 {
			t.Fatalf("got %#v", got.Temperature)
		}
	})
}

func TestBuildRequest_cacheBreakpoints(t *testing.T) {
	t.Parallel()

	baseIn := func() *modelv1.StreamCompletionRequest {
		return &modelv1.StreamCompletionRequest{
			ModelId: "m",
			Messages: []*contentv1.Message{
				{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("Hi")}},
				{Role: contentv1.Role_ROLE_ASSISTANT, Content: []*contentv1.ContentBlock{content.Text("reply")}},
				{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("thanks")}},
			},
			Tools: []*modelv1.ToolDeclaration{{Name: "t", InputSchema: schemapkg.String()}},
			AssembledContext: []*contentv1.ContextSection{
				{Provider: "p", Label: "L", Content: []*contentv1.ContentBlock{content.Text("ctx")}},
			},
		}
	}

	t.Run("every variant applies its cache_control marker", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		in.CacheBreakpoints = []*modelv1.CacheBreakpoint{
			{Position: &modelv1.CacheBreakpoint_AfterAssembledContext_{AfterAssembledContext: &modelv1.CacheBreakpoint_AfterAssembledContext{}}},
			{Position: &modelv1.CacheBreakpoint_AfterTools_{AfterTools: &modelv1.CacheBreakpoint_AfterTools{}}},
			{Position: &modelv1.CacheBreakpoint_AfterMessageIndex{AfterMessageIndex: 1}},
		}
		got, err := BuildRequest(in, fullSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.System[len(got.System)-1].CacheControl == nil {
			t.Fatal("expected cache_control on last system block")
		}
		if got.Tools[len(got.Tools)-1].CacheControl == nil {
			t.Fatal("expected cache_control on last tool")
		}
		// Message index 1 (the assistant message) isn't merged with any
		// neighbor here (roles alternate), so it survives coalescing at
		// the same position.
		assistant := got.Messages[1]
		if assistant.Role != roleAssistant || assistant.Content[len(assistant.Content)-1].CacheControl == nil {
			t.Fatalf("expected cache_control on message index 1's last block, got %#v", assistant)
		}
	})

	t.Run("more than four breakpoints is rejected", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		for range 5 {
			in.CacheBreakpoints = append(in.CacheBreakpoints, &modelv1.CacheBreakpoint{
				Position: &modelv1.CacheBreakpoint_AfterMessageIndex{AfterMessageIndex: 0},
			})
		}
		if _, err := BuildRequest(in, fullSpec()); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("out-of-range message index is rejected", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		in.CacheBreakpoints = []*modelv1.CacheBreakpoint{
			{Position: &modelv1.CacheBreakpoint_AfterMessageIndex{AfterMessageIndex: 99}},
		}
		if _, err := BuildRequest(in, fullSpec()); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("ignored entirely when caching mode is not explicit markers", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		// An out-of-range index would otherwise be rejected — proving the
		// field is truly ignored, not just successfully validated.
		in.CacheBreakpoints = []*modelv1.CacheBreakpoint{
			{Position: &modelv1.CacheBreakpoint_AfterMessageIndex{AfterMessageIndex: 99}},
		}
		got, err := BuildRequest(in, minimalSpec())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, m := range got.Messages {
			for _, b := range m.Content {
				if b.CacheControl != nil {
					t.Fatalf("expected no cache_control anywhere, found one on %#v", b)
				}
			}
		}
	})

	t.Run("after_assembled_context with empty system is rejected", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		in.AssembledContext = nil
		in.CacheBreakpoints = []*modelv1.CacheBreakpoint{
			{Position: &modelv1.CacheBreakpoint_AfterAssembledContext_{AfterAssembledContext: &modelv1.CacheBreakpoint_AfterAssembledContext{}}},
		}
		if _, err := BuildRequest(in, fullSpec()); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("after_tools with no declared tools is rejected", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		in.Tools = nil
		in.CacheBreakpoints = []*modelv1.CacheBreakpoint{
			{Position: &modelv1.CacheBreakpoint_AfterTools_{AfterTools: &modelv1.CacheBreakpoint_AfterTools{}}},
		}
		if _, err := BuildRequest(in, fullSpec()); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("entry with no position set is rejected", func(t *testing.T) {
		t.Parallel()
		in := baseIn()
		in.CacheBreakpoints = []*modelv1.CacheBreakpoint{{}}
		if _, err := BuildRequest(in, fullSpec()); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})
}

func TestBuildRequest_contentBlockCapabilityGates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		block   *contentv1.ContentBlock
		spec    model.Spec
		wantErr bool
	}{
		{name: "image accepted when vision supported", block: content.Image([]byte{1, 2, 3}, "image/png"), spec: fullSpec()},
		{name: "image rejected when vision unsupported", block: content.Image([]byte{1, 2, 3}, "image/png"), spec: minimalSpec(), wantErr: true},
		{name: "document accepted when supported", block: content.Document([]byte("pdf"), "application/pdf"), spec: fullSpec()},
		{name: "document rejected when unsupported", block: content.Document([]byte("pdf"), "application/pdf"), spec: minimalSpec(), wantErr: true},
		{
			name:  "tool_use accepted when supported",
			block: content.ToolUse("tc1", "t", mustStruct(t, map[string]any{"a": 1})),
			spec:  fullSpec(),
		},
		{
			name:    "tool_use rejected when unsupported",
			block:   content.ToolUse("tc1", "t", mustStruct(t, map[string]any{"a": 1})),
			spec:    minimalSpec(),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &modelv1.StreamCompletionRequest{
				ModelId:  "m",
				Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{tc.block}}},
			}
			_, err := BuildRequest(in, tc.spec)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildRequest_thinkingAndRedactedThinkingRawBytesUntouched(t *testing.T) {
	t.Parallel()

	sig := []byte("YmFzZTY0LXNpZ25hdHVyZQ==")
	data := []byte("cmVkYWN0ZWQtcGF5bG9hZA==")
	in := &modelv1.StreamCompletionRequest{
		ModelId: "m",
		Messages: []*contentv1.Message{
			{Role: contentv1.Role_ROLE_ASSISTANT, Content: []*contentv1.ContentBlock{
				content.Thinking("reasoning", sig),
				content.RedactedThinking(data),
			}},
		},
	}
	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	blocks := got.Messages[0].Content
	if blocks[0].Signature != string(sig) {
		t.Fatalf("signature: got %q, want %q", blocks[0].Signature, string(sig))
	}
	if blocks[1].Data != string(data) {
		t.Fatalf("data: got %q, want %q", blocks[1].Data, string(data))
	}
}

func TestBuildRequest_imageAndDocumentDataAreBase64Encoded(t *testing.T) {
	t.Parallel()

	imgData := []byte{0x89, 0x50, 0x4e, 0x47}
	docData := []byte("%PDF-1.4 ...")
	in := &modelv1.StreamCompletionRequest{
		ModelId: "m",
		Messages: []*contentv1.Message{
			{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{
				content.Image(imgData, "image/png"),
				content.Document(docData, "application/pdf", content.WithFilename("spec.pdf")),
			}},
		},
	}
	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	blocks := got.Messages[0].Content
	if blocks[0].Source.Data != base64.StdEncoding.EncodeToString(imgData) {
		t.Fatalf("image data not base64-encoded: %q", blocks[0].Source.Data)
	}
	if blocks[1].Source.Data != base64.StdEncoding.EncodeToString(docData) {
		t.Fatalf("document data not base64-encoded: %q", blocks[1].Source.Data)
	}
	if blocks[1].Title != "spec.pdf" {
		t.Fatalf("document title: got %q, want %q", blocks[1].Title, "spec.pdf")
	}
}

func TestBuildRequest_toolUseArgumentsAreDeterministicJSON(t *testing.T) {
	t.Parallel()

	args := mustStruct(t, map[string]any{"path": "main.go", "recursive": true})
	in := &modelv1.StreamCompletionRequest{
		ModelId: "m",
		Messages: []*contentv1.Message{
			{Role: contentv1.Role_ROLE_ASSISTANT, Content: []*contentv1.ContentBlock{content.ToolUse("tc1", "read_file", args)}},
		},
	}
	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotArgs := decodeJSON(t, got.Messages[0].Content[0].Input)
	wantArgs := map[string]any{"path": "main.go", "recursive": true}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("got %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestBuildRequest_systemSectionWithNonTextBlockIsRejected(t *testing.T) {
	t.Parallel()

	in := &modelv1.StreamCompletionRequest{
		ModelId:  "m",
		Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}},
		AssembledContext: []*contentv1.ContextSection{
			{Provider: "p", Label: "L", Content: []*contentv1.ContentBlock{content.Image([]byte{1}, "image/png")}},
		},
	}
	if _, err := BuildRequest(in, fullSpec()); err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestBuildRequest_messageRoleUnspecifiedIsRejected(t *testing.T) {
	t.Parallel()

	in := &modelv1.StreamCompletionRequest{
		ModelId:  "m",
		Messages: []*contentv1.Message{{Content: []*contentv1.ContentBlock{content.Text("hi")}}},
	}
	if _, err := BuildRequest(in, fullSpec()); err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestBuildRequest_emptyAssembledContextLeavesSystemNil(t *testing.T) {
	t.Parallel()

	in := &modelv1.StreamCompletionRequest{
		ModelId:  "m",
		Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("hi")}}},
	}
	got, err := BuildRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.System != nil {
		t.Fatalf("expected nil System, got %#v", got.System)
	}
}

// TestStructToJSON_isByteIdenticalAcrossRuns exists to stop a future edit
// from reintroducing protojson (or any other non-deterministic marshaler)
// into structToJSON. structpb.Struct.AsMap() returns a native Go map, whose
// own iteration order is randomized, so structToJSON is only deterministic
// because it lets encoding/json sort the keys during marshaling. If this
// test ever starts failing, the fix is in structToJSON, never in the test:
// the real-world failure mode is a tool call's arguments serializing
// differently turn to turn, which silently and permanently disables
// Anthropic's byte-exact prompt cache from that point forward — with no
// error or warning anywhere to reveal why.
func TestStructToJSON_isByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()

	s := mustStruct(t, map[string]any{
		"zulu":    map[string]any{"nested_a": 1, "nested_b": "two"},
		"yankee":  2,
		"xray":    "three",
		"whiskey": true,
		"victor":  map[string]any{"deep": map[string]any{"deeper": "value"}},
		"uniform": []any{1, 2, 3},
		"tango":   4.5,
		"sierra":  "six",
		"romeo":   false,
		"quebec":  7,
		"papa":    "eight",
	})

	first, err := structToJSON(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range 100 {
		got, err := structToJSON(s)
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", i, err)
		}
		if string(got) != string(first) {
			t.Fatalf("run %d produced different bytes than run 0:\nrun 0: %s\nrun %d: %s", i, first, i, got)
		}
	}
}

func TestStructToJSON_nilStruct(t *testing.T) {
	t.Parallel()

	got, err := structToJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("got %q, want {}", got)
	}
}

func TestTranslateRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    contentv1.Role
		want    string
		wantErr bool
	}{
		{name: "user", role: contentv1.Role_ROLE_USER, want: roleUser},
		{name: "assistant", role: contentv1.Role_ROLE_ASSISTANT, want: roleAssistant},
		{name: "unspecified", role: contentv1.Role_ROLE_UNSPECIFIED, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := translateRole(tc.role)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCoalesceMessages(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		if got := coalesceMessages(nil); got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})

	t.Run("alternating roles do not merge", func(t *testing.T) {
		t.Parallel()
		in := []Message{
			{Role: roleUser, Content: []Block{{Type: blockText, Text: "a"}}},
			{Role: roleAssistant, Content: []Block{{Type: blockText, Text: "b"}}},
			{Role: roleUser, Content: []Block{{Type: blockText, Text: "c"}}},
		}
		got := coalesceMessages(in)
		if len(got) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(got), got)
		}
	})

	t.Run("consecutive same-role messages merge", func(t *testing.T) {
		t.Parallel()
		in := []Message{
			{Role: roleUser, Content: []Block{{Type: blockText, Text: "a"}}},
			{Role: roleUser, Content: []Block{{Type: blockText, Text: "b"}}},
			{Role: roleUser, Content: []Block{{Type: blockText, Text: "c"}}},
		}
		got := coalesceMessages(in)
		want := []Message{
			{Role: roleUser, Content: []Block{
				{Type: blockText, Text: "a"},
				{Type: blockText, Text: "b"},
				{Type: blockText, Text: "c"},
			}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})
}

// TestBuildCountTokensRequest_carriesToolsAndSystem is the regression
// guard for what the request-shaped CountTokens RPC exists to fix. The
// earlier flat-text shape could carry neither tool schemas nor the system
// preamble, so a count omitted both — and tool schemas are frequently the
// single largest contributor to a request's input tokens, which is exactly
// the weight that decides whether a turn fits in the context window.
func TestBuildCountTokensRequest_carriesToolsAndSystem(t *testing.T) {
	t.Parallel()

	in := &modelv1.CountTokensRequest{
		ModelId:  "claude-opus-5",
		Messages: []*contentv1.Message{{Role: contentv1.Role_ROLE_USER, Content: []*contentv1.ContentBlock{content.Text("what changed?")}}},
		AssembledContext: []*contentv1.ContextSection{{
			Provider: "project-context",
			Label:    "CLAUDE.md",
			Content:  []*contentv1.ContentBlock{content.Text("house rules")},
		}},
		Tools: []*modelv1.ToolDeclaration{{
			Name:        "read",
			Description: "read a file",
		}},
	}

	got, err := BuildCountTokensRequest(in, fullSpec())
	if err != nil {
		t.Fatalf("BuildCountTokensRequest: %v", err)
	}

	if got.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want claude-opus-5", got.Model)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(got.Messages))
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "read" {
		t.Errorf("Tools = %+v, want the declared read tool", got.Tools)
	}
	if len(got.System) != 1 || !strings.Contains(got.System[0].Text, "house rules") {
		t.Errorf("System = %+v, want the assembled context section", got.System)
	}
}

// TestBuildCountTokensRequest_emptyRequestIsValid proves an empty request
// is a legal thing to count: most vendors bill some fixed request
// overhead, so the answer is not necessarily zero and the adapter must not
// short-circuit it.
func TestBuildCountTokensRequest_emptyRequestIsValid(t *testing.T) {
	t.Parallel()

	got, err := BuildCountTokensRequest(&modelv1.CountTokensRequest{ModelId: "claude-opus-5"}, fullSpec())
	if err != nil {
		t.Fatalf("BuildCountTokensRequest: %v", err)
	}
	if got.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want claude-opus-5", got.Model)
	}
	if len(got.Messages) != 0 || len(got.Tools) != 0 || len(got.System) != 0 {
		t.Errorf("got %+v, want an empty body apart from the model", got)
	}
}
