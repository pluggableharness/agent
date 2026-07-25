package telemetry

import (
	"go.opentelemetry.io/otel/attribute"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Attribute keys specific to PluggableHarness Agent's kernel — namespaced pluggableharness.* since
// no existing OTel semantic-convention group covers them. Where a
// semantic-convention key already exists (service.name, gen_ai.*), this
// package uses it directly (resource.go, span.go, usage.go) instead of
// duplicating it here.
//
// Cardinality rule (load-bearing — see CLAUDE.md): SessionIDKey,
// SessionParentIDKey, SessionRootIDKey, TurnIndexKey, TurnIDKey, and
// PlanItemIDKey are unbounded and MUST only ever be attached to spans, never
// used as a metric attribute. Every other key here is low-cardinality (a
// fixed enum, or bounded by the operator's configured tool/model set) and is
// safe on both.
var (
	// ProducerCategoryKey, ProducerNameKey, and ProducerVersionKey identify
	// which plugin a span concerns. The identity itself comes from the
	// kernel-authenticated source described in kernel-callbacks.md §4/§5 —
	// never a client-supplied field.
	ProducerCategoryKey = attribute.Key("pluggableharness.producer.category")
	ProducerNameKey     = attribute.Key("pluggableharness.producer.name")
	ProducerVersionKey  = attribute.Key("pluggableharness.producer.version")

	// ProviderLocalNameKey is a plugin's agent.hcl required_providers
	// local name — the operator's own label for it, distinct from
	// ProducerNameKey (the name the plugin publishes for itself). It is
	// the only identity available before a plugin has answered Describe,
	// which is why the plugin bring-up span carries it. Bounded by the
	// operator's own required_providers block, so it is safe on metrics
	// as well as spans.
	ProviderLocalNameKey = attribute.Key("pluggableharness.provider.local_name")

	// SessionIDKey, SessionParentIDKey, and SessionRootIDKey describe a
	// session's place in the RunSession tree (agent-loop.md §7).
	SessionIDKey       = attribute.Key("pluggableharness.session.id")
	SessionParentIDKey = attribute.Key("pluggableharness.session.parent_id")
	SessionRootIDKey   = attribute.Key("pluggableharness.session.root_id")

	// SessionDepthKey is the sub-agent nesting depth. Unlike the ID keys
	// above, this is low-cardinality (bounded by max_depth) and is safe on
	// metrics.
	SessionDepthKey = attribute.Key("pluggableharness.session.depth")

	AgentProfileKey = attribute.Key("pluggableharness.agent.profile")

	// SessionStatusKey is a session's terminal SessionStatus
	// (state-backend.md#session_meta), in the same lowercase snake_case
	// text form internal/statebackend stores in the status column. Bounded
	// to the fixed 7-value enum below (internal/statebackend's own mapping
	// is unexported, and importing it here would cycle back into this
	// package, which internal/statebackend already imports — so this
	// package keeps its own copy of the same spec-derived vocabulary), so
	// it's safe on both spans and metrics.
	SessionStatusKey = attribute.Key("pluggableharness.session.status")

	// TurnIndexKey is unbounded (see the cardinality rule above) — span
	// attribute only.
	TurnIndexKey = attribute.Key("pluggableharness.turn.index")

	// TurnIDKey is a turn's stable ULID identifier (standardized across the
	// whole protocol — turn-algorithm.md, context/data-types.md's
	// ContextRequest.turn_id, plan.v1's turn_id field), distinct from
	// TurnIndexKey's loop-iteration ordinal. Unbounded (see the cardinality
	// rule above) — span attribute only.
	TurnIDKey = attribute.Key("pluggableharness.turn.id")

	// PlanItemIDKey is a PlanItem's assigned id
	// (state-backend.md#plan_items). Unbounded (see the cardinality rule
	// above) — span attribute only.
	PlanItemIDKey = attribute.Key("pluggableharness.plan_item.id")

	// HookPointKey is one of the 9 named hook points (agent-loop.md §1).
	HookPointKey = attribute.Key("pluggableharness.hook.point")

	// SubscriberModeKey is "observe", "transform", or "veto"
	// (agent-loop.md §4).
	SubscriberModeKey = attribute.Key("pluggableharness.subscriber.mode")

	// ToolNameKey and ToolKindKey describe a resolved tool call. Both are
	// bounded by the operator's configured tool set / the fixed ToolKind
	// enum, so both are safe on metrics.
	ToolNameKey = attribute.Key("pluggableharness.tool.name")
	ToolKindKey = attribute.Key("pluggableharness.tool.kind")

	// ModelIDKey names the model a call targeted. Bounded by the
	// operator's required_providers set, so it's safe on metrics too.
	ModelIDKey = attribute.Key("pluggableharness.model.id")

	// AttemptKey is the retry attempt number within one model call,
	// bounded by configuration/settings-and-global.md's max_retries
	// default (5) — low-cardinality, safe on metrics too, though currently
	// only used as a span attribute (StartModelAttempt).
	AttemptKey = attribute.Key("pluggableharness.attempt")

	// PolicyDecisionKey is one of "allow", "ask", "deny"
	// (agent-loop.md §5.2).
	PolicyDecisionKey = attribute.Key("pluggableharness.policy.decision")

	// BoundKey names which LoopBounds dimension fired (agent-loop.md
	// §3.1).
	BoundKey = attribute.Key("pluggableharness.bound")

	// OutcomeKey is a generic ok/error result classifier.
	OutcomeKey = attribute.Key("pluggableharness.outcome")

	// TokenTypeKey distinguishes input/output/cache_read/cache_write on
	// the Tokens counter.
	TokenTypeKey = attribute.Key("pluggableharness.token.type")

	// FilePathKey is the filesystem path a local file-load operation
	// (config, global config, lock file, checksum) read from. Unbounded
	// (see the cardinality rule above) — span attribute only, same
	// reasoning as SessionIDKey.
	FilePathKey = attribute.Key("pluggableharness.file.path")

	// PlatformKey is the OS/arch pair (e.g. "linux_amd64") a checksum
	// verification targeted. Low-cardinality — bounded by the set of
	// platforms a provider ships binaries for — so it's safe on both
	// spans and metrics.
	PlatformKey = attribute.Key("pluggableharness.platform")

	// EventBusTopicKey is the topic an internal/eventbus.Event was
	// published on. Unbounded — a topic is caller-chosen, arbitrary
	// string — so, per the cardinality rule above, span attribute only,
	// never a metric attribute.
	EventBusTopicKey = attribute.Key("pluggableharness.eventbus.topic")

	// TokenCountFallbackReasonKey classifies why a CountTokens resolution
	// (kernel-callbacks.md#counttokens) fell back to the heuristic formula
	// instead of an exact vendor count. Bounded to the fixed 4-value enum
	// below, so it's safe on both spans and metrics. Deliberately excludes
	// the provider name — that's a higher-cardinality dimension that
	// belongs on a span (ProducerNameKey via StartKernelCallbackCountTokens),
	// never on this metric attribute.
	TokenCountFallbackReasonKey = attribute.Key("pluggableharness.tokencount.fallback_reason")

	// ContextViolationReasonKey classifies why internal/contextassembly
	// discarded a context provider's Contribute contribution for a
	// context-assemble firing (context/data-types.md#ordering--chaining's
	// scope-violation rule, context/data-types.md#budget-mechanics'
	// budget-violation rule, and context/conformance.md's non-text content
	// rejection). Bounded to the fixed 3-value enum below, so it's safe on
	// both spans and metrics. Deliberately excludes the provider name, same
	// reasoning as TokenCountFallbackReasonKey above — that belongs on a
	// span (ProducerNameKey via StartContextProviderContribute), never on
	// this metric attribute.
	ContextViolationReasonKey = attribute.Key("pluggableharness.context.violation_reason")
)

// Token type values for TokenTypeKey.
const (
	TokenTypeInput      = "input"
	TokenTypeOutput     = "output"
	TokenTypeCacheRead  = "cache_read"
	TokenTypeCacheWrite = "cache_write"
)

// Hook point values for HookPointKey — the 9 points named in
// agent-loop.md §1, in loop order.
const (
	HookPointSessionStart      = "session-start"
	HookPointContextAssemble   = "context-assemble"
	HookPointPreModelCall      = "pre-model-call"
	HookPointPostModelResponse = "post-model-response"
	HookPointPreToolCall       = "pre-tool-call"
	HookPointPlanReady         = "plan-ready"
	HookPointPostToolCall      = "post-tool-call"
	HookPointPostApply         = "post-apply"
	HookPointSessionEnd        = "session-end"
)

// Subscriber mode values for SubscriberModeKey (agent-loop.md §4).
const (
	SubscriberModeObserve   = "observe"
	SubscriberModeTransform = "transform"
	SubscriberModeVeto      = "veto"
)

// Tool kind values for ToolKindKey (tool.md's ToolKind vocabulary).
const (
	ToolKindResource    = "resource"
	ToolKindDataSource  = "data_source"
	ToolKindInteractive = "interactive"
)

// Policy decision values for PolicyDecisionKey (agent-loop.md §5.2).
const (
	PolicyDecisionAllow = "allow"
	PolicyDecisionAsk   = "ask"
	PolicyDecisionDeny  = "deny"
)

// Bound values for BoundKey (agent-loop.md §3.1).
const (
	BoundMaxTurns      = "max_turns"
	BoundMaxCostUSD    = "max_cost_usd"
	BoundMaxWallClockS = "max_wall_clock_s"
)

// Outcome values for OutcomeKey.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Session status values for SessionStatusKey — the lowercase snake_case
// text form state-backend.md#session_meta's status column documents,
// mirrored from internal/statebackend's (unexported) sessionStatusText.
const (
	SessionStatusRunning            = "running"
	SessionStatusCompleted          = "completed"
	SessionStatusErrorMaxTurns      = "error_max_turns"
	SessionStatusErrorMaxBudgetUSD  = "error_max_budget_usd"
	SessionStatusErrorMaxWallClockS = "error_max_wall_clock"
	SessionStatusCancelled          = "cancelled"
	SessionStatusFailed             = "failed"
)

// Token-count fallback reason values for TokenCountFallbackReasonKey — why
// CountTokens (kernel-callbacks.md#counttokens) used the fallback heuristic
// (determinism.md's fallback-token-heuristic section) instead of a real
// vendor count.
const (
	// FallbackReasonNoModelRef is the request had no model_ref set at all.
	FallbackReasonNoModelRef = "no_model_ref"
	// FallbackReasonProviderAbsent is the named model_ref's provider is not
	// currently loaded/reachable.
	FallbackReasonProviderAbsent = "provider_absent"
	// FallbackReasonUnimplemented is the provider is reachable but does not
	// implement the optional CountTokens RPC.
	FallbackReasonUnimplemented = "unimplemented"
	// FallbackReasonError is the provider's CountTokens RPC returned an
	// error.
	FallbackReasonError = "error"
)

// Context-assemble violation reason values for ContextViolationReasonKey —
// why internal/contextassembly discarded a provider's contribution for a
// context-assemble firing.
const (
	// ContextViolationReasonScope is a non-compactor provider's Contribute
	// response mutated, reordered, or dropped a section it does not own
	// (context/data-types.md#ordering--chaining) — its entire response was
	// discarded and the prior chain restored.
	ContextViolationReasonScope = "scope"
	// ContextViolationReasonBudget is a provider's own section exceeded its
	// allocated token_budget (context/data-types.md#budget-mechanics) — that
	// section was dropped, not the provider's whole response.
	ContextViolationReasonBudget = "budget"
	// ContextViolationReasonNonText is a provider's own section contained a
	// non-text content block, which v1 of the protocol rejects rather than
	// silently drops (context/data-types.md#contextsection).
	ContextViolationReasonNonText = "non_text"
)

// producerAttributes returns the standard three-attribute set identifying
// a plugin, for attaching to a span. Returns nil for a nil producer (a
// kernel-internal call site with no plugin to attribute to, e.g. the
// policy engine's own veto decision) — appending a nil slice is a no-op,
// so callers can unconditionally append the result.
func producerAttributes(producer *commonv1.ProducerRef) []attribute.KeyValue {
	if producer == nil {
		return nil
	}
	return []attribute.KeyValue{
		ProducerCategoryKey.String(producer.GetCategory().String()),
		ProducerNameKey.String(producer.GetName()),
		ProducerVersionKey.String(producer.GetVersion()),
	}
}
