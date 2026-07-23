package telemetry

import (
	"go.opentelemetry.io/otel/attribute"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Attribute keys specific to PluggableHarness Agent's kernel — namespaced pluggableharness.agent.* since
// no existing OTel semantic-convention group covers them. Where a
// semantic-convention key already exists (service.name, gen_ai.*), this
// package uses it directly (resource.go, span.go, usage.go) instead of
// duplicating it here.
//
// Cardinality rule (load-bearing — see CLAUDE.md): SessionIDKey,
// SessionParentIDKey, SessionRootIDKey, and TurnIndexKey are unbounded and
// MUST only ever be attached to spans, never used as a metric attribute.
// Every other key here is low-cardinality (a fixed enum, or bounded by the
// operator's configured tool/model set) and is safe on both.
var (
	// ProducerCategoryKey, ProducerNameKey, and ProducerVersionKey identify
	// which plugin a span concerns. The identity itself comes from the
	// kernel-authenticated source described in kernel-callbacks.md §4/§5 —
	// never a client-supplied field.
	ProducerCategoryKey = attribute.Key("pluggableharness.agent.producer.category")
	ProducerNameKey     = attribute.Key("pluggableharness.agent.producer.name")
	ProducerVersionKey  = attribute.Key("pluggableharness.agent.producer.version")

	// SessionIDKey, SessionParentIDKey, and SessionRootIDKey describe a
	// session's place in the RunSession tree (agent-loop.md §7).
	SessionIDKey       = attribute.Key("pluggableharness.agent.session.id")
	SessionParentIDKey = attribute.Key("pluggableharness.agent.session.parent_id")
	SessionRootIDKey   = attribute.Key("pluggableharness.agent.session.root_id")

	// SessionDepthKey is the sub-agent nesting depth. Unlike the ID keys
	// above, this is low-cardinality (bounded by max_depth) and is safe on
	// metrics.
	SessionDepthKey = attribute.Key("pluggableharness.agent.session.depth")

	AgentProfileKey = attribute.Key("pluggableharness.agent.agent.profile")

	// TurnIndexKey is unbounded (see the cardinality rule above) — span
	// attribute only.
	TurnIndexKey = attribute.Key("pluggableharness.agent.turn.index")

	// HookPointKey is one of the 9 named hook points (agent-loop.md §1).
	HookPointKey = attribute.Key("pluggableharness.agent.hook.point")

	// SubscriberModeKey is "observe", "transform", or "veto"
	// (agent-loop.md §4).
	SubscriberModeKey = attribute.Key("pluggableharness.agent.subscriber.mode")

	// ToolNameKey and ToolKindKey describe a resolved tool call. Both are
	// bounded by the operator's configured tool set / the fixed ToolKind
	// enum, so both are safe on metrics.
	ToolNameKey = attribute.Key("pluggableharness.agent.tool.name")
	ToolKindKey = attribute.Key("pluggableharness.agent.tool.kind")

	// ModelIDKey names the model a call targeted. Bounded by the
	// operator's required_providers set, so it's safe on metrics too.
	ModelIDKey = attribute.Key("pluggableharness.agent.model.id")

	// PolicyDecisionKey is one of "allow", "ask", "deny"
	// (agent-loop.md §5.2).
	PolicyDecisionKey = attribute.Key("pluggableharness.agent.policy.decision")

	// BoundKey names which LoopBounds dimension fired (agent-loop.md
	// §3.1).
	BoundKey = attribute.Key("pluggableharness.agent.bound")

	// OutcomeKey is a generic ok/error result classifier.
	OutcomeKey = attribute.Key("pluggableharness.agent.outcome")

	// TokenTypeKey distinguishes input/output/cache_read/cache_write on
	// the Tokens counter.
	TokenTypeKey = attribute.Key("pluggableharness.agent.token.type")

	// FilePathKey is the filesystem path a local file-load operation
	// (config, global config, lock file, checksum) read from. Unbounded
	// (see the cardinality rule above) — span attribute only, same
	// reasoning as SessionIDKey.
	FilePathKey = attribute.Key("pluggableharness.agent.file.path")

	// PlatformKey is the OS/arch pair (e.g. "linux_amd64") a checksum
	// verification targeted. Low-cardinality — bounded by the set of
	// platforms a provider ships binaries for — so it's safe on both
	// spans and metrics.
	PlatformKey = attribute.Key("pluggableharness.agent.platform")
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
