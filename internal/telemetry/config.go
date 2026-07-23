package telemetry

import (
	"fmt"
	"time"
)

// Config is this package's own configuration shape. internal/config (the
// agent.hcl decoder) translates its config.Observability into this Config;
// this package has no HCL/cty dependency, keeping it a leaf per
// go-architecture.md's import-direction rule.
type Config struct {
	// Enabled is the master on/off switch (settings.telemetry,
	// configuration.md §9). When false, the caller is expected to wire
	// the noop backend regardless of Backend/Endpoint — this package
	// doesn't enforce that itself, since it has no driver-name knowledge.
	Enabled bool

	// Backend names the driver a caller should select via drivers.New:
	// "otlpgrpc", "otlphttp", "stdout", "noop", or "fake". This package
	// only carries the name; it never imports the drivers subpackage
	// (that would invert go-layout.md's parent/driver import direction).
	Backend string

	// Endpoint is the OTLP collector address: host:port for the gRPC
	// driver, a base URL for the HTTP driver. Ignored by stdout/noop/fake.
	Endpoint string

	// Insecure disables TLS on the OTLP connection, for a local/dev
	// collector without certificates.
	Insecure bool

	// SamplingRatio is the ratio argument to
	// sdktrace.ParentBased(sdktrace.TraceIDRatioBased(...)). 1.0 samples
	// everything; see this package's CLAUDE.md for why ParentBased is
	// mandatory (it's what keeps a plugin span's sampling decision
	// consistent with its kernel parent across the process boundary).
	SamplingRatio float64

	// TracesEnabled, MetricsEnabled, and LogsEnabled gate each signal
	// independently of Backend/Enabled: a disabled signal is wired to an
	// OTel no-op provider and never touches the backend for that signal
	// at all.
	TracesEnabled  bool
	MetricsEnabled bool
	LogsEnabled    bool

	// ExportInterval is the metric PeriodicReader's push cadence.
	ExportInterval time.Duration

	// ServiceName and ServiceVersion populate the OTel Resource's
	// service.name/service.version.
	ServiceName    string
	ServiceVersion string

	// ResourceAttrs are additional static resource attributes an operator
	// configured (observability.resource_attrs, configuration.md §9).
	ResourceAttrs map[string]string
}

// DefaultConfig is applied when an operator's agent.hcl has no
// observability{} sub-block at all (configuration.md §9): telemetry on,
// OTLP/gRPC to a local collector, full sampling, all three signals on, a
// 10s metrics export cadence.
var DefaultConfig = Config{
	Enabled:        true,
	Backend:        "otlpgrpc",
	Endpoint:       "localhost:4317",
	Insecure:       true,
	SamplingRatio:  1.0,
	TracesEnabled:  true,
	MetricsEnabled: true,
	LogsEnabled:    true,
	ExportInterval: 10 * time.Second,
	ServiceName:    "pluggableharness-agent",
}

// Validate reports whether cfg's fields are individually well-formed:
// SamplingRatio in [0, 1], a non-negative ExportInterval, and a non-empty
// ServiceName when Enabled. It deliberately does not check Backend against
// the known driver names — that's drivers.New's job, so this package never
// needs to know the driver name set.
func (cfg Config) Validate() error {
	if cfg.SamplingRatio < 0 || cfg.SamplingRatio > 1 {
		return fmt.Errorf("telemetry: config: %w: sampling_ratio %v not in [0, 1]", ErrInvalidConfig, cfg.SamplingRatio)
	}
	if cfg.ExportInterval < 0 {
		return fmt.Errorf("telemetry: config: %w: export_interval %v is negative", ErrInvalidConfig, cfg.ExportInterval)
	}
	if cfg.Enabled && cfg.ServiceName == "" {
		return fmt.Errorf("telemetry: config: %w: service_name is required when enabled", ErrInvalidConfig)
	}
	return nil
}
