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
		{Name: "default_hook_timeout_ms", Required: false},
		{Name: "default_tool_timeout_ms", Required: false},
		{Name: "max_depth", Required: false},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "retry"},
		{Type: "observability"},
		{Type: "event_bus"},
		{Type: "doom_loop"},
	},
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

// eventBusSchema deliberately does NOT follow retrySchema's and
// observabilitySchema's all-required-within-the-block convention.
// blocks-reference.md#event_bus declares exactly one attribute, so there
// is no partial-specification case for an all-or-nothing rule to guard
// against: an absent subscribe_queue_bound is indistinguishable from an
// absent event_bus{} block, and both resolve to DefaultEventBus.
var eventBusSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "subscribe_queue_bound", Required: false},
	},
}

// doomLoopSchema also opts out of the all-or-nothing convention, for a
// different reason than eventBusSchema: turn-algorithm.md#doom-loop-detection
// states a MUST-level default for each of window_size and threshold
// individually, which is incompatible with requiring both to be declared
// together. Each attribute independently falls back to
// DefaultDoomLoopSettings.
var doomLoopSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "window_size", Required: false},
		{Name: "threshold", Required: false},
	},
}

var validLogLevels = map[string]bool{
	"trace": true, "debug": true, "info": true, "warn": true, "error": true,
}

var validObservabilityProtocols = map[string]bool{
	"grpc": true, "http": true,
}

// defaultSettings is the fully-defaulted Settings every decode path starts
// from: the one place the canonical "no settings{} block at all" values
// live, shared by load.go's decode() and decodeSettings below so the two
// paths can never drift apart. MaxDepth is deliberately left nil — see
// Settings.MaxDepth for why it is not defaulted in this package.
func defaultSettings() Settings {
	return Settings{
		Retry:                DefaultRetrySettings,
		Observability:        DefaultObservability,
		EventBus:             DefaultEventBus,
		DoomLoop:             DefaultDoomLoopSettings,
		DefaultHookTimeoutMS: DefaultHookTimeoutMS,
		DefaultToolTimeoutMS: DefaultToolTimeoutMS,
	}
}

// decodeSettings decodes settings{} (configuration.md §9). A missing
// retry{}/observability{}/event_bus{}/doom_loop{} sub-block gets its
// Default* values, not a zero-valued struct — the canonical defaults exist
// precisely so a bare agent.hcl works untuned.
func decodeSettings(body hcl.Body) (Settings, error) {
	content, diags := body.Content(settingsSchema)
	if diags.HasErrors() {
		return Settings{}, fmt.Errorf("config: settings: %w", diags)
	}

	settings := defaultSettings()

	var err error
	if settings.DefaultFrontend, err = attrString(content.Attributes["default_frontend"]); err != nil {
		return Settings{}, fmt.Errorf("config: settings: default_frontend: %w", err)
	}
	if settings.LogLevel, err = attrString(content.Attributes["log_level"]); err != nil {
		return Settings{}, fmt.Errorf("config: settings: log_level: %w", err)
	}
	if !validLogLevels[settings.LogLevel] {
		return Settings{}, fmt.Errorf("config: settings: log_level: %w: %q", ErrInvalidValue, settings.LogLevel)
	}
	if settings.Telemetry, err = attrBool(content.Attributes["telemetry"]); err != nil {
		return Settings{}, fmt.Errorf("config: settings: telemetry: %w", err)
	}

	if err := decodeSettingsOptionalAttrs(content.Attributes, &settings); err != nil {
		return Settings{}, err
	}

	for _, block := range content.Blocks {
		if err := decodeSettingsBlock(block, &settings); err != nil {
			return Settings{}, err
		}
	}

	return settings, nil
}

// decodeSettingsOptionalAttrs applies settings{}'s optional flat
// attributes onto settings, leaving each field at its default when the
// attribute is absent.
func decodeSettingsOptionalAttrs(attrs hcl.Attributes, settings *Settings) error {
	if attr, ok := attrs["default_hook_timeout_ms"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return fmt.Errorf("config: settings: default_hook_timeout_ms: %w", err)
		}
		settings.DefaultHookTimeoutMS = v
	}
	if attr, ok := attrs["default_tool_timeout_ms"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return fmt.Errorf("config: settings: default_tool_timeout_ms: %w", err)
		}
		settings.DefaultToolTimeoutMS = v
	}
	if attr, ok := attrs["max_depth"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return fmt.Errorf("config: settings: max_depth: %w", err)
		}
		settings.MaxDepth = &v
	}
	return nil
}

// decodeSettingsBlock dispatches one settings{} sub-block onto settings.
func decodeSettingsBlock(block *hcl.Block, settings *Settings) error {
	switch block.Type {
	case "retry":
		retry, err := decodeRetry(block.Body)
		if err != nil {
			return err
		}
		settings.Retry = retry
	case "observability":
		observability, err := decodeObservability(block.Body)
		if err != nil {
			return err
		}
		settings.Observability = observability
	case "event_bus":
		eventBus, err := decodeEventBus(block.Body)
		if err != nil {
			return err
		}
		settings.EventBus = eventBus
	case "doom_loop":
		doomLoop, err := decodeDoomLoop(block.Body)
		if err != nil {
			return err
		}
		settings.DoomLoop = doomLoop
	}
	return nil
}

// decodeEventBus decodes an event_bus{} sub-block. Its single attribute is
// optional: an event_bus{} block declaring nothing is equivalent to no
// event_bus{} block at all, both yielding DefaultEventBus.
func decodeEventBus(body hcl.Body) (EventBus, error) {
	content, diags := body.Content(eventBusSchema)
	if diags.HasErrors() {
		return EventBus{}, fmt.Errorf("config: settings.event_bus: %w", diags)
	}

	eventBus := DefaultEventBus
	if attr, ok := content.Attributes["subscribe_queue_bound"]; ok {
		bound, err := attrInt(attr)
		if err != nil {
			return EventBus{}, fmt.Errorf("config: settings.event_bus: subscribe_queue_bound: %w", err)
		}
		eventBus.SubscribeQueueBound = bound
	}
	return eventBus, nil
}

// decodeDoomLoop decodes a doom_loop{} sub-block. Both attributes are
// independently optional, each falling back to DefaultDoomLoopSettings —
// see doomLoopSchema for why this block opts out of the all-or-nothing
// convention retry{} and observability{} follow.
func decodeDoomLoop(body hcl.Body) (DoomLoopSettings, error) {
	content, diags := body.Content(doomLoopSchema)
	if diags.HasErrors() {
		return DoomLoopSettings{}, fmt.Errorf("config: settings.doom_loop: %w", diags)
	}

	doomLoop := DefaultDoomLoopSettings
	if attr, ok := content.Attributes["window_size"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return DoomLoopSettings{}, fmt.Errorf("config: settings.doom_loop: window_size: %w", err)
		}
		doomLoop.WindowSize = v
	}
	if attr, ok := content.Attributes["threshold"]; ok {
		v, err := attrInt(attr)
		if err != nil {
			return DoomLoopSettings{}, fmt.Errorf("config: settings.doom_loop: threshold: %w", err)
		}
		doomLoop.Threshold = v
	}
	return doomLoop, nil
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
