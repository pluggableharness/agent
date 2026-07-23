package config

import (
	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/agentprofile"
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
