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
	// CountTokens returns an exact input-token count for req against the
	// real vendor tokenizer, per
	// docs/specifications/model/protocol.md#counttokens.
	//
	// req names a whole request — messages, assembled context, and tool
	// declarations — not a string, because that is what every vendor's
	// counting endpoint actually measures, and because tool schemas are
	// frequently the largest single contributor to a request's input
	// tokens. req.ModelId MUST be honored: a provider serving several
	// models MAY use a distinct tokenizer per model.
	//
	// Takes the generated request type directly, for the same reason
	// StreamCompletion does (see this package's doc.go): it is already the
	// canonical shape, and mirroring it would be a purely duplicative copy.
	CountTokens(ctx context.Context, req *modelv1.CountTokensRequest) (int64, error)
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
	// Thinking is this model's extended-reasoning capability. Use the zero
	// ThinkingSpec{} when unsupported.
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
//
// These are independent axes, not one-of-N modes. A model may reason
// adaptively AND expose an effort ladder, or accept an effort level AND a
// deprecated token budget. Declare every control the model actually
// accepts; the kernel validates each requested parameter against the
// specific control that governs it.
type ThinkingSpec struct {
	// Supported reports whether this model has any extended-reasoning
	// capability at all. When false, Effort and Budget MUST both be nil,
	// AdaptiveByDefault MUST be false, and Disable MUST be
	// THINKING_DISABLE_SUPPORT_NEVER.
	Supported bool
	// Effort is the named-effort-level control, non-nil iff this model
	// accepts one. Nil means sending a thinking effort to this model is a
	// kernel-level reject rather than something forwarded to the vendor.
	Effort *EffortControl
	// Budget is the explicit-token-budget control, non-nil iff this model
	// accepts one. A model that never had one, and a model whose vendor
	// removed it, both leave this nil.
	Budget *BudgetControl
	// AdaptiveByDefault reports whether omitting every thinking control
	// still produces reasoning. False means an unconfigured request
	// reasons zero tokens.
	AdaptiveByDefault bool
	// Disable reports whether, and when, reasoning can be turned off. MUST
	// be set when Supported is true.
	Disable modelv1.ThinkingDisableSupport
}

// EffortControl declares that a model accepts a named reasoning-effort
// level, and which levels.
type EffortControl struct {
	// Levels are the selectable effort levels, e.g. ["low","medium",
	// "high","xhigh","max"]. MUST be non-empty — a model with no
	// selectable levels leaves ThinkingSpec.Effort nil instead.
	Levels []string
	// Default is the level the vendor applies when a request omits effort
	// entirely. MUST be non-empty and MUST appear in Levels.
	Default string
}

// BudgetControl declares that a model accepts an explicit reasoning-token
// budget, and its bounds.
type BudgetControl struct {
	// Range is the accepted token-budget range, inclusive on both bounds.
	Range ThinkingBudgetRange
	// Default is the budget the vendor applies when a request omits one.
	// Nil means the vendor reasons zero tokens by default — a pointer
	// because a declared budget of 0 and an undeclared default are
	// different statements.
	Default *int64
	// Deprecated reports that the vendor still honors this control but
	// steers callers to effort/adaptive instead, and may remove it in a
	// later model. A vendor that has already removed it is declared by
	// leaving ThinkingSpec.Budget nil, not by setting this.
	Deprecated bool
}

// ThinkingBudgetRange bounds the token budget a caller may request on a
// model declaring a BudgetControl. Both bounds are inclusive.
type ThinkingBudgetRange struct {
	// Min is the smallest thinking-token budget this model accepts.
	Min int64
	// Max is the largest thinking-token budget this model accepts.
	Max int64
}

// CachingSpec describes one model's prompt-caching capability, per
// docs/specifications/model/data-types.md#cachingspec.
//
// ExplicitMarkers and ImplicitAutomatic are independent axes, not
// alternatives: a model may run automatic caching by default and still
// accept explicit breakpoints at a deeper discount. Declare both when
// both are true.
type CachingSpec struct {
	// Supported reports whether this model has any prompt-caching
	// capability at all. When false, both axes below MUST be false; when
	// true, at least one MUST be true.
	Supported bool
	// ExplicitMarkers reports whether the caller may place cache
	// breakpoints that this adapter translates into vendor-native markers.
	// This is the axis StreamCompletionRequest.cache_breakpoints is gated
	// on — an adapter for a model without it MUST ignore that field rather
	// than error on it.
	ExplicitMarkers bool
	// ImplicitAutomatic reports whether the vendor caches transparently
	// above some token threshold with no caller action. Declaring it
	// requires nothing of the kernel; it exists so cache-hit and cost
	// behavior are explicable.
	ImplicitAutomatic bool
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
	// RateLimits is the vendor's own rate-limit state as of this
	// completion. MAY be empty; a Provider MUST NOT synthesize a snapshot
	// from its own bookkeeping — only report what the vendor published.
	RateLimits []RateLimitSnapshot
}

// RateLimitSnapshot is one of the vendor's rate-limit budgets as of one
// completion, per docs/specifications/model/data-types.md#usagerate_limits.
//
// Every field but Kind is a pointer because vendors publish different
// subsets, and "the vendor did not say" is meaningfully different from
// "the vendor said zero" — the second means the budget is exhausted.
type RateLimitSnapshot struct {
	// Kind names which budget this describes. MUST be set.
	Kind modelv1.RateLimitKind
	// Remaining is how much of this budget is left.
	Remaining *int64
	// Limit is this budget's ceiling for the current window.
	Limit *int64
	// ResetAt is when this budget next resets.
	ResetAt *time.Time
}
