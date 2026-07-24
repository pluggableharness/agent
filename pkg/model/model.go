package model

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Provider is the interface a model-provider plugin author implements — the
// three MUST RPCs (docs/specifications/model/conformance.md's summary
// matrix): GetCapabilities, Configure, and StreamCompletion.
//
// A Provider MAY additionally implement TokenCounter (CountTokens, SHOULD)
// and/or Renderer (Render, MAY) — see NewService's doc comment for how
// server.go detects these.
type Provider interface {
	// Capabilities returns this plugin's advertised model list and
	// provider-wide declarations, per
	// docs/specifications/model/protocol.md#getcapabilities. MUST be cheap
	// to call repeatedly and MUST NOT require a network call to the vendor
	// if avoidable — a Provider SHOULD build its model list once (e.g. a
	// package-level literal assembled in a constructor) and return it here
	// rather than querying the vendor per call.
	Capabilities(ctx context.Context) (*Capabilities, error)

	// Configure accepts the provider's agent.hcl config block, already
	// decoded from HCL/cty into a Struct by the kernel's schema-to-cty
	// bridge, per docs/specifications/model/protocol.md#configure. MUST
	// reject a missing required field with a structured error (a
	// *Error with category invalid_request or auth_error, as
	// appropriate) rather than deferring the failure to the first
	// StreamCompletion call. MUST NOT echo any received secret value into
	// an Emit'd event, a Render output, a log line, or an error message.
	Configure(ctx context.Context, config *structpb.Struct) error

	// StreamCompletion generates one completion, writing every event
	// through sink. Per docs/specifications/model/protocol.md#streamcompletion,
	// a backend that does not natively stream MUST still emit its full
	// response as a single terminal burst of events followed by a Stop —
	// there is no separate non-streaming code path. Cancellation
	// (ctx.Done(), or sink detecting the kernel closed the stream) MUST be
	// treated as normal control flow: stop generating, release resources,
	// and return ctx.Err() (or nil after already having sent a Stop with
	// STOP_REASON_CANCELLED) rather than surfacing it as an *Error.
	StreamCompletion(ctx context.Context, req *modelv1.StreamCompletionRequest, sink *Sink) error
}

// TokenCounter is the optional interface behind CountTokens
// (docs/specifications/model/protocol.md#counttokens, SHOULD). server.go
// type-asserts a Provider against this interface at call time and returns
// codes.Unimplemented when a Provider doesn't implement it, letting the
// kernel fall back to the documented heuristic
// (docs/specifications/kernel-callbacks.md#the-fallback-heuristic) — this
// mirrors the Go standard library's optional-interface pattern (e.g.
// io.ReaderFrom) rather than adding a boolean capability flag a Provider
// must remember to keep in sync with its own method set.
type TokenCounter interface {
	// CountTokens returns an exact token count for text against modelID's
	// real vendor tokenizer, per
	// docs/specifications/model/protocol.md#counttokens. modelID MUST be
	// honored — a provider serving several models MAY use a distinct
	// tokenizer per model.
	CountTokens(ctx context.Context, text, modelID string) (int64, error)
}

// Renderer is the optional interface behind Render
// (docs/specifications/model/protocol.md#render, MAY). Detected the same
// way as TokenCounter; a Provider that doesn't implement it gets
// codes.Unimplemented from server.go, and the kernel falls back to its
// generic default rendering.
type Renderer interface {
	// Render decodes payload — emitted under schemaVersion — into a
	// RenderTree, per docs/specifications/model/protocol.md#render and
	// docs/specifications/frontend/render-tree.md#schema-versioning. A
	// Provider with more than one schema_version in its history typically
	// implements this by dispatching through a
	// pkg/render.NewVersionRegistry.
	Render(ctx context.Context, payload []byte, schemaVersion string) (*renderv1.RenderTree, error)
}

// Capabilities is GetCapabilities' response payload, per
// docs/specifications/model/data-types.md#modelspec and
// docs/specifications/model/data-types.md#capabilitiessupported_hook_points
// — the domain mirror of modelv1.Capabilities.
type Capabilities struct {
	// Models is one Spec per model this plugin can serve. MUST have
	// at least one entry — NewCapabilities rejects an empty slice.
	Models []Spec
	// SlashCommands are prompt-expansion slash commands this provider
	// contributes, declared once for the provider as a whole. MAY be
	// empty. Passed through as the generated common.v1 type directly —
	// see doc.go's rationale for not mirroring pass-through nested types.
	SlashCommands []*commonv1.PromptExpansionSpec
	// ConfigSchema is this provider's agent.hcl config schema, typically
	// built with pkg/config. MUST be present.
	ConfigSchema *configv1.ConfigSchema
	// SupportedHookPoints declares which hook points this plugin can serve
	// via HookSubscriberService.DispatchHook. MAY be empty.
	SupportedHookPoints []commonv1.HookPoint
}

// Spec describes one model this provider can serve, per
// docs/specifications/model/data-types.md#modelspec. Every field is MUST
// unless its comment says otherwise.
type Spec struct {
	// ID is the vendor's exact model identifier, used to select this model
	// in StreamCompletionRequest.model_id.
	ID string
	// ContextWindow is the model's input token budget.
	ContextWindow int64
	// MaxOutputTokens is the model's maximum output tokens per response.
	MaxOutputTokens int64
	// SupportsToolUse reports whether this model accepts tool
	// declarations and can emit tool_use content blocks.
	SupportsToolUse bool
	// SupportsVision reports whether this model accepts image content
	// blocks.
	SupportsVision bool
	// SupportsStreaming is a UX hint only — the StreamCompletion RPC shape
	// is always server-streaming regardless of this value, per
	// docs/specifications/model/README.md#transport--lifecycle.
	SupportsStreaming bool
	// SupportsParallelToolCalls reports whether this model can return
	// multiple tool_use blocks in a single turn. SHOULD be set
	// accurately; false means the kernel serializes tool calls for this
	// model.
	SupportsParallelToolCalls bool
	// Thinking is this model's extended-reasoning capability. Use
	// ThinkingSpec{} (Mode left at THINKING_MODE_NONE) when unsupported.
	Thinking ThinkingSpec
	// Caching is this model's prompt-caching capability. Use
	// CachingSpec{} (Mode left at CACHING_MODE_NONE) when unsupported.
	Caching CachingSpec
	// Pricing is this model's cost structure. MUST be present even for a
	// free model (set Pricing.Free = true).
	Pricing Pricing
	// SupportedToolChoiceModes declares which GenerationParams.tool_choice.mode
	// values this model accepts. Empty means this model cannot constrain
	// tool choice at all.
	SupportedToolChoiceModes []modelv1.ToolChoiceMode
	// SupportsDocuments reports whether this model accepts a
	// DocumentBlock content block.
	SupportsDocuments bool
}

// ThinkingSpec describes one model's extended-reasoning capability, per
// docs/specifications/model/data-types.md#thinkingspec.
type ThinkingSpec struct {
	// Supported reports whether this model has any extended-reasoning
	// capability at all.
	Supported bool
	// Mode is which reasoning-control shape this model uses. MUST be
	// THINKING_MODE_NONE when Supported is false.
	Mode modelv1.ThinkingMode
	// EffortLevels are the selectable effort levels, e.g. ["low","medium",
	// "high","xhigh","max"]. MUST be non-empty when
	// Mode == THINKING_MODE_DISCRETE_EFFORT.
	EffortLevels []string
	// BudgetRange is the selectable token-budget range. MUST be present
	// when Mode == THINKING_MODE_CONTINUOUS_BUDGET.
	BudgetRange *ThinkingBudgetRange
	// CanDisable reports whether reasoning can be turned off once
	// enabled. Some vendors' reasoning cannot be disabled.
	CanDisable bool
	// Default is the effort level (discrete_effort) or budget-token value
	// (continuous_budget), as a string, the vendor applies when a request
	// omits thinking config entirely. MUST be non-empty when
	// Mode != THINKING_MODE_NONE.
	Default string
}

// ThinkingBudgetRange bounds the token budget a caller may request when
// ThinkingSpec.Mode is THINKING_MODE_CONTINUOUS_BUDGET.
type ThinkingBudgetRange struct {
	// Min is the smallest thinking-token budget this model accepts.
	Min int64
	// Max is the largest thinking-token budget this model accepts.
	Max int64
}

// CachingSpec describes one model's prompt-caching capability, per
// docs/specifications/model/data-types.md#cachingspec.
type CachingSpec struct {
	// Supported reports whether this model has any prompt-caching
	// capability at all.
	Supported bool
	// Mode is which caching mechanic this model uses. MUST be
	// CACHING_MODE_NONE when Supported is false.
	Mode modelv1.CachingMode
	// KeepaliveSupported reports whether this provider runs its own
	// cache-keepalive loop. MUST be set (default false); cache TTL
	// mechanics are vendor-specific and provider-owned, never a kernel
	// loop — see the field's doc comment on the generated type.
	KeepaliveSupported bool
}

// Pricing describes one model's cost structure, per
// docs/specifications/model/data-types.md#pricing. MUST be present on
// every Spec, even a free one.
type Pricing struct {
	// Currency is the pricing currency. MUST be "USD" for v1
	// (docs/specifications/model/conformance.md's open questions notes
	// this as a real, if narrow, v1 constraint — no multi-currency
	// aggregation exists yet).
	Currency string
	// Free is true for a local/free-to-run model. When true, Tiers MAY be
	// empty.
	Free bool
	// Tiers are this model's rate tiers. MUST have at least one entry
	// unless Free is true. Exactly one tier MUST match any given
	// (timestamp, input_token_count) pair — see capabilities.go's
	// validatePricing for the overlap check NewCapabilities performs.
	Tiers []PricingTier
}

// PricingTier is one time-bounded, input-size-bounded rate within a
// model's Pricing, per docs/specifications/model/data-types.md#pricing.
// Every *float64/*int64/*time.Time field is a pointer specifically because
// nil vs. zero is meaningful on the wire (an omitted bound is unbounded on
// that side, per the field's own doc comment) — unlike ThinkingSpec.Default
// or CachingSpec.KeepaliveSupported above, where the domain type could
// safely collapse the generated type's optionality into a plain zero-value
// field.
type PricingTier struct {
	// EffectiveFrom is the moment this tier becomes active. Nil means
	// "since this plugin version was published".
	EffectiveFrom *time.Time
	// EffectiveUntil is the moment this tier stops being active. Nil
	// means "still current".
	EffectiveUntil *time.Time
	// InputPerMtok is the cost per million input tokens, realtime rate.
	InputPerMtok float64
	// OutputPerMtok is the cost per million output tokens, realtime rate.
	OutputPerMtok float64
	// CacheWritePerMtok is the cost per million cache-write tokens. MUST
	// be present iff the owning Spec.Caching.Supported.
	CacheWritePerMtok *float64
	// CacheReadPerMtok is the cost per million cache-read tokens. MUST be
	// present iff the owning Spec.Caching.Supported.
	CacheReadPerMtok *float64
	// BatchInputPerMtok is a vendor's discounted batch/async input rate,
	// where one exists. MAY be present.
	BatchInputPerMtok *float64
	// BatchOutputPerMtok is a vendor's discounted batch/async output
	// rate, paired with BatchInputPerMtok. MAY be present.
	BatchOutputPerMtok *float64
	// InputTokensFrom is the smallest accumulated-input-token count this
	// tier applies to, inclusive. Nil means unbounded below.
	InputTokensFrom *int64
	// InputTokensUntil is the input-token count this tier stops applying
	// to, exclusive. Nil means unbounded above.
	InputTokensUntil *int64
}

// Usage carries token accounting for one completion, per
// docs/specifications/model/data-types.md#streamevent. Passed to
// Sink.Usage. CacheReadTokens/CacheWriteTokens/ReasoningTokens are
// pointers because the vendor not reporting a distinct count (nil) is
// meaningfully different from the vendor reporting zero of that kind.
type Usage struct {
	// InputTokens is the input tokens consumed by this completion.
	InputTokens int64
	// OutputTokens is the output tokens produced by this completion.
	OutputTokens int64
	// CacheReadTokens is tokens read from cache, when the model supports
	// caching. Never also counted in InputTokens.
	CacheReadTokens *int64
	// CacheWriteTokens is tokens written to cache, when the model
	// supports caching. Never also counted in InputTokens.
	CacheWriteTokens *int64
	// ReasoningTokens is thinking/reasoning tokens, when the vendor
	// reports them as a distinct count. Never also counted in
	// OutputTokens; billed at PricingTier.OutputPerMtok.
	ReasoningTokens *int64
}
