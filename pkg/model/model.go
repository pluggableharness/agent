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

// Accounter is the optional interface a Provider implements to report
// live account and entitlement state, per
// docs/specifications/model/protocol.md#getaccount (MAY).
//
// Implement it when the backend meters against something an operator can
// run out of — a subscription pool, a credit balance, a plan quota — so
// the harness can show headroom before a turn strands them. A Provider
// that does not implement it returns codes.Unimplemented, which the
// kernel treats as "no account state", not an error.
//
// Do not implement this by counting tokens locally: the point is what
// the vendor says is left, and a locally-derived figure is the
// synthesized value RateLimitSnapshot's own contract forbids.
type Accounter interface {
	// GetAccount reads current account state from the vendor. Called on
	// demand rather than cached by the kernel, so an implementation
	// SHOULD apply its own short cache if the upstream call is
	// expensive or rate-limited.
	GetAccount(ctx context.Context) (AccountSnapshot, error)
}

// AccountSnapshot is the live account and entitlement state behind a
// provider's credential.
type AccountSnapshot struct {
	// Method is the credential shape in use.
	Method modelv1.AuthMethod
	// Metering is what completions are charged against.
	Metering modelv1.MeteringDomain
	// Plan is the vendor's plan name, where a subscription names one.
	// Display only.
	Plan *string
	// Labels are non-secret, vendor-defined labels — a redacted account
	// handle, a region, an organization name. MUST NOT carry key
	// material, tokens, or a full account identifier.
	Labels map[string]string
	// Quotas are the budgets the vendor publishes for this account
	// outside a completion. MAY be empty.
	Quotas []RateLimitSnapshot
	// FetchedAt is when this snapshot was read from the vendor, so a
	// frontend can show how stale it is rather than presenting a cached
	// reading as live.
	FetchedAt *time.Time
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
	// Auth describes how this plugin authenticated and which pool it
	// meters against. Nil for a provider with one credential shape and
	// nothing to disambiguate.
	Auth *AuthDescriptor
	// CatalogEtag is the vendor's version identifier for the model
	// catalog this roster was built from, when the provider fetched one.
	// Reported so staleness is detectable; the kernel does not yet
	// re-fetch capabilities on a mismatch.
	CatalogEtag *string
	// CatalogFetchedAt is when this roster was fetched. Nil for a
	// hand-written roster compiled into the plugin — which is the
	// distinction it exists to make: a static roster is never stale.
	CatalogFetchedAt *time.Time
}

// AuthDescriptor is the non-secret description of how a plugin is
// authenticated.
//
// Nothing here may be a credential or derived from one in a way that
// leaks it: no key material, no token, no full account identifier. That
// applies to Labels too.
type AuthDescriptor struct {
	// Method is the credential shape in use.
	Method modelv1.AuthMethod
	// Metering is what completions are charged against.
	Metering modelv1.MeteringDomain
	// Plan is the vendor's plan name, where a subscription names one.
	// Display only; the kernel never routes on it.
	Plan *string
	// Labels are additional non-secret, vendor-defined labels — a
	// redacted account handle, a region, an organization name.
	Labels map[string]string
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

	// Catalog is human-facing metadata for a model picker. Nil for a
	// hand-written roster with nothing to say beyond the id. Purely
	// descriptive: a kernel ignoring it routes and bills identically.
	Catalog *CatalogMetadata
	// MaxContextWindow is the largest window this model can be
	// configured with, where the vendor exposes a ceiling above
	// ContextWindow. ContextWindow remains what the kernel budgets
	// against.
	MaxContextWindow *int64
	// EffectiveContextWindowPercent is the fraction of ContextWindow,
	// 0-100, usable after the vendor's own fixed overhead. Nil means the
	// whole window is usable.
	EffectiveContextWindowPercent *float64
	// AutoCompactTokenLimit is the assembled-token count at which the
	// vendor recommends compacting. Advisory only.
	AutoCompactTokenLimit *int64
	// Verbosity is the model's output-verbosity control, where it
	// exposes one distinct from thinking effort.
	Verbosity *VerbositySpec
	// ServiceTiers are the tiers this model can be served at, in the
	// vendor's naming. Empty means no tier choice.
	ServiceTiers []string
	// APIBackend names which vendor API surface serves this model
	// ("chat_completions", "responses"). Opaque to the kernel.
	APIBackend *string
	// TruncationPolicy is the vendor's policy name for truncating
	// oversized tool output. Opaque to the kernel.
	TruncationPolicy *string
	// CompHash is the vendor's compaction-compatibility identifier.
	// Models sharing a value can consume each other's compacted history.
	CompHash *string
}

// CatalogMetadata is the human-facing description of a model — what a
// picker shows, not what the kernel routes on.
type CatalogMetadata struct {
	// DisplayName is the model's display name ("Grok 4.5"). Nil means a
	// frontend falls back to Spec.ID.
	DisplayName *string
	// Description is a one-line description of what the model is for.
	Description *string
	// Visible reports whether a picker should offer this model by
	// default. Nil means visible.
	Visible *bool
	// Priority is a sort weight for a picker, higher first. Nil means
	// unranked.
	Priority *int32
	// SupportedInAPI reports whether an API key can reach this model, as
	// opposed to only a product session. With AuthDescriptor.Method it
	// lets a frontend hide models the current credential cannot use.
	SupportedInAPI *bool
	// Aliases are other ids resolving to this same model. Do NOT also
	// publish an alias as its own Spec — that is what makes one model
	// look like several.
	Aliases []string
	// Family groups variants differing only by size or revision.
	Family *string
}

// VerbositySpec declares a model's output-verbosity control — answer
// length, as distinct from ThinkingSpec's reasoning depth.
type VerbositySpec struct {
	// Supported reports whether this model accepts a verbosity setting.
	// When false, Levels MUST be empty and Default nil.
	Supported bool
	// Levels are the accepted level names, least to most verbose.
	Levels []string
	// Default is the level applied when a request names none. MUST be
	// one of Levels when set.
	Default *string
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
	// SupportsReasoningSummary reports whether this model can emit a
	// reasoning summary distinct from its raw reasoning stream.
	SupportsReasoningSummary *bool
	// DefaultReasoningSummary is the summary mode applied when a request
	// names none ("auto", "concise", "detailed"). Meaningless unless
	// SupportsReasoningSummary is true.
	DefaultReasoningSummary *string
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
	// SourceUnit records the vendor's own pricing unit these rates were
	// converted from, when the adapter converted. Audit only — the
	// kernel bills from the per-MTok rates regardless. Set it when you
	// convert, so a ledger figure disagreeing with an invoice can be
	// traced to the rate or the arithmetic.
	SourceUnit *string
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
	// ImageInputPerMtok is the price per million image input tokens,
	// where the vendor rates image input separately. Nil means image
	// input bills at InputPerMtok.
	ImageInputPerMtok *float64
	// AudioInputPerMtok is the same for audio input.
	AudioInputPerMtok *float64
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

	// VendorCost is what the vendor says this completion cost, in the
	// vendor's own denomination. Nil when the vendor reports no figure.
	//
	// Reported, never authoritative: the kernel still computes cost_usd
	// from the token counts above and the model's PricingTier, and every
	// rollup and budget reads that. Set this when the vendor publishes a
	// price so the two can be reconciled — do not compute it yourself.
	VendorCost *VendorCost

	// VendorTotalTokens is the vendor's own total-token figure, when it
	// publishes one that is not simply the sum of the parts above. Leave
	// nil rather than filling it in by addition; a derived value here
	// destroys the disagreement the field exists to expose.
	VendorTotalTokens *int64

	// Components are vendor-defined counters with no first-class field:
	// per-modality input tokens, accepted/rejected prediction tokens,
	// hosted-tool source counts. The kernel stores and surfaces these
	// without interpreting them.
	Components []UsageComponent

	// ReasoningAlreadyCounted reports that ReasoningTokens is already
	// included in OutputTokens, because the vendor said so out of band.
	// Nil means "not stated" and the kernel applies the documented
	// default (a distinct count). Set it only on an explicit vendor
	// signal — the field exists to stop the kernel double-counting, so a
	// guess defeats it.
	ReasoningAlreadyCounted *bool
}

// VendorCost is a vendor's own price for one completion, in whatever
// unit that vendor bills in.
type VendorCost struct {
	// Amount is the cost as an exact decimal string ("0.00241",
	// "24100000"). A string rather than a float because these are exact
	// monetary quantities that binary floating point cannot represent
	// exactly. MUST parse as a decimal number, with no currency symbol,
	// separators, or exponent.
	Amount string
	// Unit names the vendor's denomination: "usd", "xai_ticks_1e10".
	// The kernel never converts between units.
	Unit string
	// Currency is the ISO 4217 code, when Unit is currency-denominated
	// and the vendor bills in something other than USD.
	Currency *string
}

// UsageComponent is one vendor-defined counter the protocol has no typed
// field for.
type UsageComponent struct {
	// Name is the counter's vendor-facing name, verbatim
	// ("input_image_tokens", "num_sources_used"). MUST be set and unique
	// within one Usage.
	Name string
	// Value is the counter's value. Not necessarily a token count —
	// "num_sources_used" counts documents.
	Value int64
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
	// LimitID is the vendor's stable identifier for this budget, where
	// it names one ("codex", "codex_other"). It lets two snapshots of
	// the same budget be correlated across completions even when Kind
	// and WindowRole match.
	LimitID *string
	// LimitName is a human-facing label, when the vendor supplies one
	// worth showing. Never synthesize it from LimitID — a frontend falls
	// back to Kind and WindowRole perfectly well, and an invented label
	// reads as authoritative.
	LimitName *string
	// WindowRole distinguishes the several budgets a subscription
	// product meters at once — which is the headline limit and which
	// constrain bursts inside it.
	WindowRole modelv1.WindowRole
	// UsedPercent is how much of this budget is spent, 0-100, for
	// products that publish only a percentage.
	//
	// Set this instead of faking Limit=100 and Remaining=100-percent to
	// fit the absolute fields. A vendor publishing real counts sets
	// Remaining/Limit and leaves this nil; one publishing a percentage
	// sets this and leaves those nil. Never derive one form from the
	// other.
	UsedPercent *float64
	// WindowSeconds is the budget window's length. With ResetAt it lets
	// a frontend say "5 hours" rather than only "resets at 14:00".
	WindowSeconds *int64
}
