package config

import (
	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/doomloop"
	"github.com/pluggableharness/agent/internal/policy"
)

// RequiredProvider is one required_providers entry (configuration.md §5).
type RequiredProvider struct {
	// Source is the git-forge address, e.g.
	// "github.com/agentco/provider-anthropic". MUST be a github.com or
	// gitlab.com address per configuration.md §5.
	Source string

	// Constraint is the raw version-constraint string, e.g. "~> 1.2.3",
	// using the same operators as Terraform (=, !=, >, >=, <, <=, ~>).
	// This package captures it as declared; parsing/resolving it against
	// real available versions is a registry concern, not this package's.
	Constraint string
}

// RetrySettings is settings.retry{}'s canonical backoff configuration
// (configuration.md §9, agent-loop.md §8.1).
type RetrySettings struct {
	BaseDelayMS   int
	BackoffFactor int
	MaxRetries    int
}

// DefaultRetrySettings are the canonical defaults applied whenever a
// retry{} sub-block (or the whole settings{} block) is absent, so a bare
// agent.hcl works untuned (configuration.md §9).
var DefaultRetrySettings = RetrySettings{BaseDelayMS: 500, BackoffFactor: 2, MaxRetries: 5}

// Observability is settings.observability{}'s OTel-specific configuration
// — a tracked correction to configuration.md §9, which defines telemetry
// as a bare on/off bool with no further shape. This is the HCL-decoded
// form; internal/telemetry.Config is the shape it translates into, kept
// deliberately free of any HCL/cty dependency (see
// internal/telemetry/CLAUDE.md).
type Observability struct {
	// Endpoint is the OTLP collector address.
	Endpoint string

	// Protocol is "grpc" or "http", selecting which OTLP transport to use.
	Protocol string

	// SamplingRatio is the ParentBased(TraceIDRatioBased) sampler's ratio,
	// in [0, 1].
	SamplingRatio float64

	// TracesEnabled, MetricsEnabled, and LogsEnabled gate each signal
	// independently.
	TracesEnabled  bool
	MetricsEnabled bool
	LogsEnabled    bool

	// ExportIntervalMS is the metric PeriodicReader's push cadence.
	ExportIntervalMS int

	// ServiceName populates the OTel Resource's service.name.
	ServiceName string

	// ResourceAttrs are additional static resource attributes an operator
	// wants attached to every span/metric this process emits.
	ResourceAttrs map[string]string
}

// DefaultObservability are the canonical defaults applied whenever an
// observability{} sub-block (or the whole settings{} block) is absent —
// same rationale as DefaultRetrySettings: telemetry on, OTLP/gRPC to a
// local collector, full sampling, all three signals on, a 10s export
// cadence.
var DefaultObservability = Observability{
	Endpoint:         "localhost:4317",
	Protocol:         "grpc",
	SamplingRatio:    1.0,
	TracesEnabled:    true,
	MetricsEnabled:   true,
	LogsEnabled:      true,
	ExportIntervalMS: 10000,
	ServiceName:      "pluggableharness-agent",
}

// EventBus is the settings.event_bus{} block
// (configuration/blocks-reference.md#event_bus).
type EventBus struct {
	// SubscribeQueueBound is the per-Subscribe-stream backpressure bound
	// (event-bus.md#backpressure): once a stream's undelivered-event queue
	// exceeds it, the kernel closes that stream with
	// codes.ResourceExhausted rather than growing the queue further.
	// Defaults to 1024 when the block — or this specific attribute — is
	// absent.
	SubscribeQueueBound int
}

// DefaultEventBus is the canonical default applied whenever an
// event_bus{} sub-block (or the whole settings{} block) is absent, so a
// bare agent.hcl runs with a bounded Subscribe queue
// (configuration/blocks-reference.md#event_bus).
var DefaultEventBus = EventBus{SubscribeQueueBound: 1024}

// DoomLoopSettings is the settings.doom_loop{} block
// (agent-loop/turn-algorithm.md#doom-loop-detection) — the kernel-owned
// repeated-call detector's tunable window and threshold.
type DoomLoopSettings struct {
	// WindowSize is how many recent call hashes the detector retains.
	WindowSize int

	// Threshold is how many consecutive identical hashes trip the
	// detector. The spec constrains it to [3, 5]; this package carries
	// what was declared and leaves the range check to doomloop.New, which
	// already owns it (ErrInvalidThreshold).
	Threshold int
}

// DefaultDoomLoopSettings is the canonical default applied whenever a
// doom_loop{} sub-block (or one of its two attributes, or the whole
// settings{} block) is absent. Its values are read from
// doomloop.DefaultConfig rather than restated, so the window/threshold
// defaults have exactly one source of truth.
var DefaultDoomLoopSettings = DoomLoopSettings{
	WindowSize: doomloop.DefaultConfig.WindowSize,
	Threshold:  doomloop.DefaultConfig.Threshold,
}

// DefaultHookTimeoutMS is the canonical default for
// Settings.DefaultHookTimeoutMS. No canonical value is given anywhere in
// the spec prose — agent-loop/hook-dispatch.md#per-subscriber-timeout
// only establishes that the knob exists and is kernel-configurable — so
// this is a project-level judgment call: a reasonable
// operator-overridable starting point in the same spirit as
// DefaultRetrySettings, not a value dictated by the spec text.
const DefaultHookTimeoutMS = 5000

// DefaultToolTimeoutMS is the canonical default for
// Settings.DefaultToolTimeoutMS. Same judgment-call reasoning as
// DefaultHookTimeoutMS: tool/protocol.md#getschema establishes only that
// the kernel has a global default a ToolSchema.default_timeout may
// override, never what that default is.
const DefaultToolTimeoutMS = 30000

// Settings is the settings{} block (configuration.md §9).
type Settings struct {
	// DefaultFrontend names which required_providers entry the CLI attaches
	// when more than one frontend provider is loaded.
	DefaultFrontend string

	// LogLevel is one of "trace", "debug", "info", "warn", "error".
	LogLevel string

	// Telemetry enables/disables telemetry reporting.
	Telemetry bool

	// Retry holds the canonical retry/backoff defaults, operator-overridable.
	Retry RetrySettings

	// Observability holds the OTel-specific tracing/metrics configuration,
	// operator-overridable.
	Observability Observability

	// EventBus holds the event-bus backpressure configuration,
	// operator-overridable.
	EventBus EventBus

	// DoomLoop holds the doom-loop detector's window/threshold,
	// operator-overridable.
	DoomLoop DoomLoopSettings

	// DefaultHookTimeoutMS is the per-hook-subscriber dispatch deadline
	// (agent-loop/hook-dispatch.md#per-subscriber-timeout), overridable
	// per subscriber via a hook{} block's own timeout_ms attribute
	// (Hook.TimeoutMS). Defaults to DefaultHookTimeoutMS when settings{}
	// or this attribute is absent — see that constant for why the value
	// is a project-level judgment call rather than a spec-mandated one.
	DefaultHookTimeoutMS int

	// DefaultToolTimeoutMS is the kernel's global Invoke deadline, applied
	// absent a ToolSchema.default_timeout override
	// (tool/protocol.md#getschema). Defaults to DefaultToolTimeoutMS when
	// settings{} or this attribute is absent — same judgment-call
	// reasoning as DefaultHookTimeoutMS.
	DefaultToolTimeoutMS int

	// MaxDepth is settings.max_depth, the kernel's configured default
	// root-session depth ceiling (agent-loop/subagents.md#depth-limits,
	// configuration/agent-profiles.md#depth-budget's "kernel's own
	// configured default"). A *int rather than an int because unset and an
	// explicit 0 are semantically different — 0 means "the root session
	// may spawn nothing at all", which is a real, declarable choice —
	// mirroring agentprofile.AgentProfile.MaxDepth's own *int shape.
	//
	// Unlike Retry/Observability/EventBus/DoomLoop, nil is deliberately
	// NOT replaced with a canonical default here: this package carries
	// what agent.hcl declared and lets the consuming call site resolve
	// nil, because agentprofile.RootRemainingDepth already resolves the
	// same "unset" case through its own kernelDefault parameter.
	// Defaulting it in both places would be redundant and could disagree.
	MaxDepth *int
}

// Hook is an explicit hook{} block (configuration.md §8.6) — a plugin
// subscribing to a hook point its category doesn't imply by default.
type Hook struct {
	// Point is the hook{} block's label, e.g. "post-tool-call".
	Point string

	// Provider is the declared provider name subscribing at Point.
	Provider string

	// Mode is one of "observe", "transform", "veto".
	Mode string

	// TimeoutMS is this hook{} block's optional per-subscriber timeout
	// override, in milliseconds
	// (agent-loop/hook-dispatch.md#per-subscriber-timeout:
	// "default_hook_timeout_ms, with a per-subscriber agent.hcl
	// override"). nil when the block doesn't declare it, in which case a
	// caller falls back to Settings.DefaultHookTimeoutMS.
	TimeoutMS *int

	// Range is this block's source position, for a caller to resolve
	// ordering against implicit subscriptions by textual declaration
	// position (configuration.md §8.6) — this package does not resolve
	// that ordering itself.
	Range hcl.Range
}

// Config is the structurally-parsed contents of one agent.hcl file.
// Provider bodies remain raw and undecoded — see DecodeProviderConfig.
type Config struct {
	// RequiredProviders is keyed by each entry's local name
	// (configuration.md §5).
	RequiredProviders map[string]RequiredProvider

	// ProviderBodies holds each provider{} block's raw, undecoded body,
	// keyed by the local name it configures. Decode via
	// DecodeProviderConfig once that provider's ConfigSchema is known.
	ProviderBodies map[string]hcl.Body

	// ProviderRanges holds each provider{} block's source position,
	// keyed the same way as ProviderBodies — for hook-ordering resolution
	// (configuration.md §8.6), which this package does not itself perform.
	ProviderRanges map[string]hcl.Range

	// Policies are every policy{} block, already validated
	// conflict-free (internal/policy.ValidateRules) before LoadFile
	// returns successfully.
	Policies []policy.Rule

	// AgentProfiles is keyed by each profile's declared name
	// (configuration.md §8).
	AgentProfiles map[string]agentprofile.AgentProfile

	// Hooks are every explicit hook{} block (configuration.md §8.6).
	Hooks []Hook

	// Settings is the settings{} block, or its zero-value-plus-defaults
	// form if the block was entirely absent (configuration.md §9).
	Settings Settings
}
