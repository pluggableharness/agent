package config

import (
	"time"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// Backend name constants for the mapping from an observability{} protocol
// onto internal/telemetry/drivers.New's driver-name set. They are named
// here rather than imported because internal/telemetry (and therefore the
// Config this package builds) deliberately carries the backend as an
// opaque string and never imports its own drivers subpackage.
const (
	backendOTLPGRPC = "otlpgrpc"
	backendOTLPHTTP = "otlphttp"
	backendNoop     = "noop"
)

// TelemetryConfig bridges this package's HCL-decoded Settings into
// internal/telemetry.Config, the OTel-native shape that package keeps
// deliberately free of any HCL/cty dependency (see internal/telemetry's
// own CLAUDE.md).
//
// settings.telemetry = false forces the discarding backend regardless of
// observability{}'s contents — no exporter is ever constructed
// (configuration/settings-and-global.md#the-telemetry-switch). The rest of
// the mapping is field-for-field from Observability, with three
// deliberate asymmetries:
//
//   - Observability.Protocol has no Config field of its own; it is
//     consumed here into Config.Backend ("grpc" -> "otlpgrpc", "http" ->
//     "otlphttp"). A protocol outside that pair maps to the empty string,
//     which drivers.New rejects with ErrUnknownDriver — deliberately loud,
//     rather than silently picking a transport the operator didn't ask
//     for. LoadFile already rejects such a value at decode time, so this
//     only matters for a hand-built Settings.
//   - Config.Insecure and Config.ServiceVersion have no Observability
//     counterpart, because blocks-reference.md#observability declares no
//     corresponding attribute. Both stay at their zero value; giving
//     either a home means adding the field to the spec's observability{}
//     table first.
//   - Settings.LogLevel is not carried: telemetry.Config has no
//     log-severity field (internal/log owns that vocabulary), so the
//     operator's level reaches its consumers by another path.
func TelemetryConfig(s Settings) telemetry.Config {
	cfg := telemetry.Config{
		Enabled:        s.Telemetry,
		Backend:        backendForProtocol(s.Observability.Protocol),
		Endpoint:       s.Observability.Endpoint,
		SamplingRatio:  s.Observability.SamplingRatio,
		TracesEnabled:  s.Observability.TracesEnabled,
		MetricsEnabled: s.Observability.MetricsEnabled,
		LogsEnabled:    s.Observability.LogsEnabled,
		ExportInterval: time.Duration(s.Observability.ExportIntervalMS) * time.Millisecond,
		ServiceName:    s.Observability.ServiceName,
		ResourceAttrs:  s.Observability.ResourceAttrs,
	}

	// The master switch wins over everything observability{} declared: the
	// noop driver still builds a real SDK pipeline but discards at the
	// export boundary, so no exporter — and no collector connection — is
	// ever constructed.
	if !s.Telemetry {
		cfg.Backend = backendNoop
	}

	return cfg
}

// backendForProtocol maps an observability{} protocol onto the driver name
// drivers.New expects. An unrecognized protocol yields "" — see
// TelemetryConfig's doc comment for why that is preferable to a fallback.
func backendForProtocol(protocol string) string {
	switch protocol {
	case "grpc":
		return backendOTLPGRPC
	case "http":
		return backendOTLPHTTP
	default:
		return ""
	}
}
