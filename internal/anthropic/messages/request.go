package messages

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Anthropic's `thinking.type` literals. Not shared with types.go's wire
// vocabulary block because these three are specific to how request.go
// drives the field, not a value ever read back off the wire.
const (
	thinkingTypeAdaptive = "adaptive"
	thinkingTypeEnabled  = "enabled"
	thinkingTypeDisabled = "disabled"
)

// maxCacheBreakpoints is Anthropic's hard cap on cache_control markers per
// request. Exceeding it is a 400 from the vendor, so BuildRequest rejects
// it up front with invalid_request rather than letting the vendor's error
// surface three layers up the stack.
const maxCacheBreakpoints = 4

// newInvalidRequestError builds the *model.Error every BuildRequest failure
// returns. Every failure here is a kernel/adapter bug — malformed input the
// kernel should never have sent — which is exactly what invalid_request
// means per docs/specifications/model/conformance.md, so Retryable is
// always false and the message carries only request-shaped data, never a
// config value or secret.
func newInvalidRequestError(format string, args ...any) *model.Error {
	return &model.Error{
		Category:  modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
		Message:   "anthropic: build request: " + fmt.Sprintf(format, args...),
		Retryable: false,
	}
}

// structToJSON converts s into deterministic JSON bytes via its native Go
// map representation.
//
// NEVER use protojson here. protojson deliberately injects
// non-deterministic whitespace into its output to discourage byte
// comparison, and Anthropic's prompt cache is a byte-exact prefix match:
// non-deterministic tool-argument serialization would silently and
// permanently disable caching for every turn after the first tool call,
// with no error anywhere to reveal why.
func structToJSON(s *structpb.Struct) (json.RawMessage, error) {
	if s == nil {
		return json.Marshal(map[string]any{})
	}
	return json.Marshal(s.AsMap())
}

// BuildRequest translates in into the Anthropic request body for the model
// described by spec.
func BuildRequest(in *modelv1.StreamCompletionRequest, spec model.Spec) (*Request, error) {
	params := in.GetParams()

	maxTokens := spec.MaxOutputTokens
	if params.GetMaxOutputTokens() > 0 {
		maxTokens = params.GetMaxOutputTokens()
	}

	system, err := buildSystem(in.GetAssembledContext())
	if err != nil {
		return nil, err
	}

	tools, err := buildTools(in.GetTools())
	if err != nil {
		return nil, err
	}

	// Translated before coalescing so cache-breakpoint message indices,
	// which are defined against the kernel's original message list, can
	// still be resolved correctly — see applyCacheBreakpoints.
	origMessages := make([]Message, len(in.GetMessages()))
	for i, m := range in.GetMessages() {
		msg, err := translateMessage(m, spec)
		if err != nil {
			return nil, err
		}
		origMessages[i] = msg
	}

	if err := applyCacheBreakpoints(in.GetCacheBreakpoints(), spec, system, tools, origMessages); err != nil {
		return nil, err
	}

	toolChoice, err := buildToolChoice(params)
	if err != nil {
		return nil, err
	}

	thinking, outputConfig, err := buildThinking(params, spec)
	if err != nil {
		return nil, err
	}

	// Models on the discrete-effort thinking ladder reject temperature
	// outright (a 400 from the vendor); models on continuous-budget
	// thinking (or with no thinking capability at all) still accept it.
	var temperature *float64
	if params != nil && params.Temperature != nil && spec.Thinking.Mode != modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT {
		t := *params.Temperature
		temperature = &t
	}

	return &Request{
		Model:         in.GetModelId(),
		MaxTokens:     maxTokens,
		Messages:      coalesceMessages(origMessages),
		Stream:        true,
		System:        system,
		Tools:         tools,
		ToolChoice:    toolChoice,
		StopSequences: params.GetStopSequences(),
		Temperature:   temperature,
		Thinking:      thinking,
		OutputConfig:  outputConfig,
	}, nil
}

// buildSystem translates the kernel-assembled context chain into
// Anthropic's top-level `system` array, one TextBlock per section. Each
// section's concatenated text is wrapped in a delimiter line built from its
// Label, so the model sees a clear boundary between one context provider's
// contribution and the next.
func buildSystem(sections []*contentv1.ContextSection) ([]TextBlock, error) {
	if len(sections) == 0 {
		return nil, nil
	}
	out := make([]TextBlock, 0, len(sections))
	for _, sec := range sections {
		var text strings.Builder
		for _, block := range sec.GetContent() {
			tb, ok := block.GetBlock().(*contentv1.ContentBlock_Text)
			if !ok {
				return nil, newInvalidRequestError("assembled context section %q contains a non-text block", sec.GetLabel())
			}
			text.WriteString(tb.Text.GetText())
		}
		label := sec.GetLabel()
		out = append(out, TextBlock{
			Type: blockText,
			Text: fmt.Sprintf("<%s>\n%s\n</%s>", label, text.String(), label),
		})
	}
	return out, nil
}

// buildTools translates every kernel ToolDeclaration into an Anthropic Tool.
func buildTools(decls []*modelv1.ToolDeclaration) ([]Tool, error) {
	if len(decls) == 0 {
		return nil, nil
	}
	tools := make([]Tool, 0, len(decls))
	for _, d := range decls {
		schema, err := schemaToJSON(d.GetInputSchema())
		if err != nil {
			return nil, newInvalidRequestError("tool %q: %s", d.GetName(), err)
		}
		tools = append(tools, Tool{
			Name:        d.GetName(),
			Description: d.GetDescription(),
			InputSchema: schema,
		})
	}
	return tools, nil
}

// buildToolChoice translates params' tool_choice, when set, into
// Anthropic's ToolChoice shape.
func buildToolChoice(params *modelv1.GenerationParams) (*ToolChoice, error) {
	tc := params.GetToolChoice()
	if tc == nil {
		return nil, nil
	}
	switch tc.GetMode() {
	case modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_AUTO:
		return &ToolChoice{Type: toolChoiceAuto}, nil
	case modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_ANY:
		return &ToolChoice{Type: toolChoiceAny}, nil
	case modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_NONE:
		return &ToolChoice{Type: toolChoiceNone}, nil
	case modelv1.ToolChoiceMode_TOOL_CHOICE_MODE_SPECIFIC:
		name := tc.GetToolName()
		if name == "" {
			return nil, newInvalidRequestError("tool_choice mode SPECIFIC requires tool_name")
		}
		return &ToolChoice{Type: toolChoiceTool, Name: name}, nil
	default:
		return nil, nil
	}
}

// buildThinking translates params' thinking-control fields into Anthropic's
// Thinking/OutputConfig pair, driven by spec's declared ThinkingSpec.Mode.
func buildThinking(params *modelv1.GenerationParams, spec model.Spec) (*Thinking, *OutputConfig, error) {
	switch spec.Thinking.Mode {
	case modelv1.ThinkingMode_THINKING_MODE_DISCRETE_EFFORT:
		if params == nil || params.ThinkingEffort == nil {
			return nil, nil, nil
		}
		level := *params.ThinkingEffort
		if !slices.Contains(spec.Thinking.EffortLevels, level) {
			return nil, nil, newInvalidRequestError("thinking effort %q is not one of model %q's effort levels %v", level, spec.ID, spec.Thinking.EffortLevels)
		}
		return &Thinking{Type: thinkingTypeAdaptive}, &OutputConfig{Effort: level}, nil

	case modelv1.ThinkingMode_THINKING_MODE_CONTINUOUS_BUDGET:
		if params == nil || params.ThinkingBudgetTokens == nil {
			return nil, nil, nil
		}
		budget := *params.ThinkingBudgetTokens
		if budget == 0 && spec.Thinking.CanDisable {
			return &Thinking{Type: thinkingTypeDisabled}, nil, nil
		}
		if spec.Thinking.BudgetRange == nil || budget < spec.Thinking.BudgetRange.Min || budget > spec.Thinking.BudgetRange.Max {
			return nil, nil, newInvalidRequestError("thinking budget %d is outside model %q's budget range", budget, spec.ID)
		}
		return &Thinking{Type: thinkingTypeEnabled, BudgetTokens: &budget}, nil, nil

	default:
		return nil, nil, nil
	}
}

// applyCacheBreakpoints translates the kernel's cache breakpoints into
// vendor-native cache_control markers, mutating system's, tools', and
// origMessages' blocks in place.
//
// origMessages MUST be the pre-coalescing, one-Message-per-kernel-message
// slice: after_message_index is defined against the kernel's original
// message list, and coalesceMessages (called after this function returns)
// changes indices by merging consecutive same-role messages — applying
// breakpoints beforehand is what keeps a breakpoint's placement correct
// regardless of how the messages are later merged.
func applyCacheBreakpoints(breakpoints []*modelv1.CacheBreakpoint, spec model.Spec, system []TextBlock, tools []Tool, origMessages []Message) error {
	if spec.Caching.Mode != modelv1.CachingMode_CACHING_MODE_EXPLICIT_MARKERS {
		// MUST ignore per StreamCompletionRequest.cache_breakpoints: this
		// field is meaningful only under explicit-marker caching, and
		// placement is a kernel decision this adapter only executes.
		return nil
	}
	if len(breakpoints) > maxCacheBreakpoints {
		return newInvalidRequestError("cache_breakpoints: %d requested, exceeds Anthropic's cap of %d per request", len(breakpoints), maxCacheBreakpoints)
	}

	ephemeral := &CacheControl{Type: cacheControlEphemeral}
	for _, bp := range breakpoints {
		switch v := bp.GetPosition().(type) {
		case *modelv1.CacheBreakpoint_AfterAssembledContext_:
			if len(system) == 0 {
				return newInvalidRequestError("cache_breakpoints: after_assembled_context set but assembled_context is empty")
			}
			system[len(system)-1].CacheControl = ephemeral

		case *modelv1.CacheBreakpoint_AfterTools_:
			if len(tools) == 0 {
				return newInvalidRequestError("cache_breakpoints: after_tools set but no tools were declared")
			}
			tools[len(tools)-1].CacheControl = ephemeral

		case *modelv1.CacheBreakpoint_AfterMessageIndex:
			idx := v.AfterMessageIndex
			if idx < 0 || idx >= int64(len(origMessages)) {
				return newInvalidRequestError("cache_breakpoints: after_message_index %d is out of range for %d messages", idx, len(origMessages))
			}
			msg := &origMessages[idx]
			if len(msg.Content) == 0 {
				return newInvalidRequestError("cache_breakpoints: after_message_index %d has no content blocks to mark", idx)
			}
			msg.Content[len(msg.Content)-1].CacheControl = ephemeral

		default:
			return newInvalidRequestError("cache_breakpoints: entry has no position set")
		}
	}
	return nil
}

// translateMessage translates one canonical content.v1.Message into an
// Anthropic Message.
func translateMessage(m *contentv1.Message, spec model.Spec) (Message, error) {
	role, err := translateRole(m.GetRole())
	if err != nil {
		return Message{}, err
	}
	blocks, err := translateBlocks(m.GetContent(), spec)
	if err != nil {
		return Message{}, err
	}
	return Message{Role: role, Content: blocks}, nil
}

// translateRole translates a canonical content.v1.Role into Anthropic's
// role string.
func translateRole(r contentv1.Role) (string, error) {
	switch r {
	case contentv1.Role_ROLE_USER:
		return roleUser, nil
	case contentv1.Role_ROLE_ASSISTANT:
		return roleAssistant, nil
	default:
		return "", newInvalidRequestError("message role is unset or unknown (%v)", r)
	}
}

// coalesceMessages merges consecutive same-role messages into one message
// whose Content is the concatenation of theirs.
//
// Anthropic's own docs are contradictory about whether the API merges
// consecutive same-role messages server-side, so this adapter does it
// itself rather than relying on undocumented vendor behavior. This is also
// what guarantees that every tool_result block answering one assistant
// turn lands in a single user message, which Anthropic documents as a firm
// requirement — the kernel may emit several ToolResultBlocks as separate
// canonical messages, and without this step they would arrive as several
// consecutive user messages instead of one.
func coalesceMessages(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Message, 0, len(msgs))
	out = append(out, msgs[0])
	for _, m := range msgs[1:] {
		last := &out[len(out)-1]
		if last.Role == m.Role {
			last.Content = append(last.Content, m.Content...)
			continue
		}
		out = append(out, m)
	}
	return out
}

// translateBlocks translates a slice of canonical content blocks in order.
func translateBlocks(blocks []*contentv1.ContentBlock, spec model.Spec) ([]Block, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		blk, err := translateBlock(b, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, blk)
	}
	return out, nil
}

// translateBlock translates one canonical content.v1.ContentBlock variant
// into an Anthropic Block, rejecting a variant the target model's spec
// doesn't support.
func translateBlock(b *contentv1.ContentBlock, spec model.Spec) (Block, error) {
	switch v := b.GetBlock().(type) {
	case *contentv1.ContentBlock_Text:
		return Block{Type: blockText, Text: v.Text.GetText()}, nil

	case *contentv1.ContentBlock_Image:
		if !spec.SupportsVision {
			return Block{}, newInvalidRequestError("model %q does not support image content blocks", spec.ID)
		}
		return Block{
			Type: blockImage,
			Source: &Source{
				Type:      sourceBase64,
				MediaType: v.Image.GetMediaType(),
				// Image bytes are raw binary and, unlike
				// ThinkingBlock.Signature/RedactedThinkingBlock.Data below,
				// genuinely need base64 encoding here.
				Data: base64.StdEncoding.EncodeToString(v.Image.GetData()),
			},
		}, nil

	case *contentv1.ContentBlock_Document:
		if !spec.SupportsDocuments {
			return Block{}, newInvalidRequestError("model %q does not support document content blocks", spec.ID)
		}
		blk := Block{
			Type: blockDocument,
			Source: &Source{
				Type:      sourceBase64,
				MediaType: v.Document.GetMediaType(),
				Data:      base64.StdEncoding.EncodeToString(v.Document.GetData()),
			},
		}
		if fn := v.Document.GetFilename(); fn != "" {
			blk.Title = fn
		}
		return blk, nil

	case *contentv1.ContentBlock_ToolUse:
		if !spec.SupportsToolUse {
			return Block{}, newInvalidRequestError("model %q does not support tool use", spec.ID)
		}
		input, err := structToJSON(v.ToolUse.GetArguments())
		if err != nil {
			return Block{}, newInvalidRequestError("tool_use %q arguments: %s", v.ToolUse.GetName(), err)
		}
		return Block{
			Type:  blockToolUse,
			ID:    v.ToolUse.GetId(),
			Name:  v.ToolUse.GetName(),
			Input: input,
		}, nil

	case *contentv1.ContentBlock_ToolResult:
		content, err := translateBlocks(v.ToolResult.GetContent(), spec)
		if err != nil {
			return Block{}, err
		}
		return Block{
			Type:      blockToolResult,
			ToolUseID: v.ToolResult.GetToolUseId(),
			Content:   content,
			IsError:   v.ToolResult.GetIsError(),
		}, nil

	case *contentv1.ContentBlock_Thinking:
		return Block{
			Type:     blockThinking,
			Thinking: v.Thinking.GetText(),
			// Signature holds the literal ASCII bytes of Anthropic's own
			// base64 signature text, carried through verbatim. Do NOT
			// base64-encode or decode it: Go's encoder isn't guaranteed to
			// reproduce the vendor's exact padding/alphabet, and any
			// deviation makes the vendor reject the next turn outright.
			Signature: string(v.Thinking.GetSignature()),
		}, nil

	case *contentv1.ContentBlock_RedactedThinking:
		return Block{
			Type: blockRedactedThinking,
			// Same do-not-re-encode rule as Signature above: Data is the
			// literal ASCII bytes of Anthropic's own encrypted blob text.
			Data: string(v.RedactedThinking.GetData()),
		}, nil

	default:
		return Block{}, newInvalidRequestError("content block has no set variant")
	}
}
