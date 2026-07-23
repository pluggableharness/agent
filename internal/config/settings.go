package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
)

var settingsSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "default_frontend", Required: true},
		{Name: "log_level", Required: true},
		{Name: "telemetry", Required: true},
	},
	Blocks: []hcl.BlockHeaderSchema{{Type: "retry"}, {Type: "observability"}},
}

var retrySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "base_delay_ms", Required: true},
		{Name: "backoff_factor", Required: true},
		{Name: "max_retries", Required: true},
	},
}

// observabilitySchema mirrors retrySchema's all-required-within-the-block
// convention for every field except resource_attrs, which is genuinely
// optional (most configs won't want extra static tags at all).
var observabilitySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "endpoint", Required: true},
		{Name: "protocol", Required: true},
		{Name: "sampling_ratio", Required: true},
		{Name: "traces_enabled", Required: true},
		{Name: "metrics_enabled", Required: true},
		{Name: "logs_enabled", Required: true},
		{Name: "export_interval_ms", Required: true},
		{Name: "service_name", Required: true},
		{Name: "resource_attrs", Required: false},
	},
}

var validLogLevels = map[string]bool{
	"trace": true, "debug": true, "info": true, "warn": true, "error": true,
}

var validObservabilityProtocols = map[string]bool{
	"grpc": true, "http": true,
}

// decodeSettings decodes settings{} (configuration.md §9). A missing
// retry{} sub-block gets DefaultRetrySettings, not a zero-valued
// RetrySettings — the canonical defaults exist precisely so a bare
// agent.hcl works untuned.
func decodeSettings(body hcl.Body) (Settings, error) {
	content, diags := body.Content(settingsSchema)
	if diags.HasErrors() {
		return Settings{}, fmt.Errorf("config: settings: %w", diags)
	}

	defaultFrontend, err := attrString(content.Attributes["default_frontend"])
	if err != nil {
		return Settings{}, fmt.Errorf("config: settings: default_frontend: %w", err)
	}
	logLevel, err := attrString(content.Attributes["log_level"])
	if err != nil {
		return Settings{}, fmt.Errorf("config: settings: log_level: %w", err)
	}
	if !validLogLevels[logLevel] {
		return Settings{}, fmt.Errorf("config: settings: log_level: %w: %q", ErrInvalidValue, logLevel)
	}
	telemetry, err := attrBool(content.Attributes["telemetry"])
	if err != nil {
		return Settings{}, fmt.Errorf("config: settings: telemetry: %w", err)
	}

	settings := Settings{
		DefaultFrontend: defaultFrontend,
		LogLevel:        logLevel,
		Telemetry:       telemetry,
		Retry:           DefaultRetrySettings,
		Observability:   DefaultObservability,
	}

	for _, block := range content.Blocks {
		switch block.Type {
		case "retry":
			retry, err := decodeRetry(block.Body)
			if err != nil {
				return Settings{}, err
			}
			settings.Retry = retry
		case "observability":
			observability, err := decodeObservability(block.Body)
			if err != nil {
				return Settings{}, err
			}
			settings.Observability = observability
		}
	}

	return settings, nil
}

func decodeRetry(body hcl.Body) (RetrySettings, error) {
	content, diags := body.Content(retrySchema)
	if diags.HasErrors() {
		return RetrySettings{}, fmt.Errorf("config: settings.retry: %w", diags)
	}

	baseDelay, err := attrInt(content.Attributes["base_delay_ms"])
	if err != nil {
		return RetrySettings{}, fmt.Errorf("config: settings.retry: base_delay_ms: %w", err)
	}
	backoff, err := attrInt(content.Attributes["backoff_factor"])
	if err != nil {
		return RetrySettings{}, fmt.Errorf("config: settings.retry: backoff_factor: %w", err)
	}
	maxRetries, err := attrInt(content.Attributes["max_retries"])
	if err != nil {
		return RetrySettings{}, fmt.Errorf("config: settings.retry: max_retries: %w", err)
	}

	return RetrySettings{BaseDelayMS: baseDelay, BackoffFactor: backoff, MaxRetries: maxRetries}, nil
}

// decodeObservability decodes an observability{} sub-block into
// Observability. resource_attrs is the one optional field — an absent
// resource_attrs decodes to a nil map, not an error.
func decodeObservability(body hcl.Body) (Observability, error) {
	content, diags := body.Content(observabilitySchema)
	if diags.HasErrors() {
		return Observability{}, fmt.Errorf("config: settings.observability: %w", diags)
	}

	endpoint, err := attrString(content.Attributes["endpoint"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: endpoint: %w", err)
	}
	protocol, err := attrString(content.Attributes["protocol"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: protocol: %w", err)
	}
	if !validObservabilityProtocols[protocol] {
		return Observability{}, fmt.Errorf("config: settings.observability: protocol: %w: %q", ErrInvalidValue, protocol)
	}
	samplingRatio, err := attrFloat(content.Attributes["sampling_ratio"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: sampling_ratio: %w", err)
	}
	tracesEnabled, err := attrBool(content.Attributes["traces_enabled"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: traces_enabled: %w", err)
	}
	metricsEnabled, err := attrBool(content.Attributes["metrics_enabled"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: metrics_enabled: %w", err)
	}
	logsEnabled, err := attrBool(content.Attributes["logs_enabled"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: logs_enabled: %w", err)
	}
	exportIntervalMS, err := attrInt(content.Attributes["export_interval_ms"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: export_interval_ms: %w", err)
	}
	serviceName, err := attrString(content.Attributes["service_name"])
	if err != nil {
		return Observability{}, fmt.Errorf("config: settings.observability: service_name: %w", err)
	}

	var resourceAttrs map[string]string
	if attr, ok := content.Attributes["resource_attrs"]; ok {
		resourceAttrs, err = attrStringMap(attr)
		if err != nil {
			return Observability{}, fmt.Errorf("config: settings.observability: resource_attrs: %w", err)
		}
	}

	return Observability{
		Endpoint:         endpoint,
		Protocol:         protocol,
		SamplingRatio:    samplingRatio,
		TracesEnabled:    tracesEnabled,
		MetricsEnabled:   metricsEnabled,
		LogsEnabled:      logsEnabled,
		ExportIntervalMS: exportIntervalMS,
		ServiceName:      serviceName,
		ResourceAttrs:    resourceAttrs,
	}, nil
}
