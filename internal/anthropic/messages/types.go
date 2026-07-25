package messages

import "encoding/json"

// Anthropic's own wire vocabulary. Every string constant below is a
// literal that appears on the vendor's wire; nothing here is a
// PluggableHarness concept.
const (
	// Block types, shared between request content and streamed
	// content_block_start payloads.
	blockText             = "text"
	blockImage            = "image"
	blockDocument         = "document"
	blockToolUse          = "tool_use"
	blockToolResult       = "tool_result"
	blockThinking         = "thinking"
	blockRedactedThinking = "redacted_thinking"

	// Source types inside an image or document block.
	sourceBase64 = "base64"

	// cache_control's only currently-defined type.
	cacheControlEphemeral = "ephemeral"

	// tool_choice types.
	toolChoiceAuto = "auto"
	toolChoiceAny  = "any"
	toolChoiceNone = "none"
	toolChoiceTool = "tool"

	// Conversation roles. Anthropic has no system role — system content
	// is the top-level `system` field, which is exactly why
	// content.v1.Role has no SYSTEM value either.
	roleUser      = "user"
	roleAssistant = "assistant"
)

// Request is the JSON body of POST /v1/messages.
//
// This is Anthropic's schema, not a second Go representation of a
// PluggableHarness wire message — see this package's CLAUDE.md for why
// that distinction matters and why go-layout.md's one-representation rule
// is not in tension with it.
//
// Every optional field is a pointer or a slice with `omitempty` so an
// unset field is absent from the JSON rather than present as a zero
// value. That is not cosmetic: Anthropic rejects `temperature` outright on
// current models, and a `"temperature": 0` emitted for an unset override
// would turn every request into a 400.
type Request struct {
	Model     string    `json:"model"`
	MaxTokens int64     `json:"max_tokens"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`

	System        []TextBlock   `json:"system,omitempty"`
	Tools         []Tool        `json:"tools,omitempty"`
	ToolChoice    *ToolChoice   `json:"tool_choice,omitempty"`
	StopSequences []string      `json:"stop_sequences,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	Thinking      *Thinking     `json:"thinking,omitempty"`
	OutputConfig  *OutputConfig `json:"output_config,omitempty"`
}

// Message is one turn in Anthropic's conversation array.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// Block is one content block. Anthropic discriminates on "type" and puts
// every variant's fields at the same level, so this is one flat struct
// with omitempty rather than a Go union — unmarshaling a discriminated
// union into a sum type would need a custom UnmarshalJSON per block, and
// buys nothing here because the adapter always knows which variant it is
// building or reading.
//
// Input is json.RawMessage rather than any: a tool call's arguments
// arrive from the kernel as a structpb.Struct and must reach the wire
// byte-for-byte identically on every turn, which means they are
// pre-serialized once by a deterministic marshaler and carried as raw
// bytes from there. See CLAUDE.md's protojson prohibition — this field is
// the reason that rule exists.
type Block struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image, document
	Source *Source `json:"source,omitempty"`
	// document only; several vendors surface it to the model as a
	// citation label.
	Title string `json:"title,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string  `json:"tool_use_id,omitempty"`
	Content   []Block `json:"content,omitempty"`
	IsError   bool    `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// redacted_thinking
	Data string `json:"data,omitempty"`

	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// TextBlock is the one block shape Anthropic accepts inside the
// top-level `system` array. Modeled separately from Block because system
// content is text-only and a shared struct would invite a caller to set
// fields the vendor rejects there.
type TextBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// Source carries inline bytes for an image or document block.
type Source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// CacheControl is the vendor-native prompt-cache marker the kernel's
// CacheBreakpoint translates into.
type CacheControl struct {
	Type string `json:"type"`
}

// Tool is one tool declaration.
//
// InputSchema is json.RawMessage for the same determinism reason as
// Block.Input: the schema is derived from a proto message containing a
// map, and it must serialize identically on every turn or Anthropic's
// prefix cache misses on every request after the first.
type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
}

// ToolChoice constrains whether and which tool the model must call.
type ToolChoice struct {
	Type string `json:"type"`
	// Name is set only when Type is "tool".
	Name string `json:"name,omitempty"`
}

// Thinking is the reasoning-control parameter. Type is "adaptive",
// "enabled", or "disabled"; BudgetTokens accompanies "enabled" only, and
// is rejected on models that dropped the manual budget form.
type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens *int64 `json:"budget_tokens,omitempty"`
}

// OutputConfig carries the effort level. It is a sibling of `format`
// (structured outputs), which this adapter does not use.
type OutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

// Usage is Anthropic's token accounting, as it appears on message_start
// and (cumulatively) on message_delta.
//
// Every count is a pointer because "the vendor did not report this" and
// "the vendor reported zero" are different facts the protocol preserves:
// model.Usage's cache and reasoning counters are pointers for exactly the
// same reason.
type Usage struct {
	InputTokens              *int64 `json:"input_tokens,omitempty"`
	OutputTokens             *int64 `json:"output_tokens,omitempty"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens,omitempty"`
}

// APIError is the error envelope Anthropic returns on a non-2xx response
// and inside a mid-stream `error` SSE event.
type APIError struct {
	Type      string       `json:"type"`
	Error     APIErrorBody `json:"error"`
	RequestID string       `json:"request_id,omitempty"`
}

// APIErrorBody is the inner object of an APIError, carrying the vendor's
// own error taxonomy string.
type APIErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Anthropic's error.type values, exhaustive as of the roster's sourcing
// date. Each maps to one HTTP status and to one
// modelv1.ModelErrorCategory — see classify.go for the table.
const (
	errInvalidRequest  = "invalid_request_error"
	errAuthentication  = "authentication_error"
	errBilling         = "billing_error"
	errPermission      = "permission_error"
	errNotFound        = "not_found_error"
	errConflict        = "conflict_error"
	errRequestTooLarge = "request_too_large"
	errRateLimit       = "rate_limit_error"
	errAPI             = "api_error"
	errTimeout         = "timeout_error"
	errOverloaded      = "overloaded_error"
)

// Streamed SSE event type names, as they appear both on the `event:` line
// and as the JSON payload's own "type" field.
const (
	eventMessageStart      = "message_start"
	eventContentBlockStart = "content_block_start"
	eventContentBlockDelta = "content_block_delta"
	eventContentBlockStop  = "content_block_stop"
	eventMessageDelta      = "message_delta"
	eventMessageStop       = "message_stop"
	eventPing              = "ping"
	eventError             = "error"
)

// content_block_delta delta.type values.
const (
	deltaText      = "text_delta"
	deltaInputJSON = "input_json_delta"
	deltaThinking  = "thinking_delta"
	deltaSignature = "signature_delta"
)

// Anthropic's stop_reason values.
const (
	stopEndTurn      = "end_turn"
	stopToolUse      = "tool_use"
	stopMaxTokens    = "max_tokens"
	stopStopSequence = "stop_sequence"
	stopRefusal      = "refusal"
	stopPauseTurn    = "pause_turn"
)

// StreamEvent is one decoded SSE event payload. Anthropic's events are a
// discriminated union on "type" with disjoint field sets, flattened here
// for the same reason as Block.
type StreamEvent struct {
	Type string `json:"type"`

	// message_start
	Message *StreamMessage `json:"message,omitempty"`

	// content_block_start / _delta / _stop
	Index        int64        `json:"index,omitempty"`
	ContentBlock *Block       `json:"content_block,omitempty"`
	Delta        *StreamDelta `json:"delta,omitempty"`

	// message_delta
	Usage *Usage `json:"usage,omitempty"`

	// error
	Error *APIErrorBody `json:"error,omitempty"`
}

// StreamMessage is the partially-populated Message object carried by
// message_start, whose only field this adapter reads is Usage.
type StreamMessage struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Usage *Usage `json:"usage,omitempty"`
}

// StreamDelta is the delta object on a content_block_delta, and — with a
// different field set — the top-level delta on a message_delta. Anthropic
// reuses the key for both, so one struct covers both.
type StreamDelta struct {
	Type string `json:"type"`

	// text_delta
	Text string `json:"text,omitempty"`
	// input_json_delta
	PartialJSON string `json:"partial_json,omitempty"`
	// thinking_delta
	Thinking string `json:"thinking,omitempty"`
	// signature_delta
	Signature string `json:"signature,omitempty"`

	// message_delta's own delta carries these two instead.
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}
