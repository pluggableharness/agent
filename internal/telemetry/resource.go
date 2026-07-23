package telemetry

import (
	"context"
	"fmt"
	"sort"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// BuildResource constructs the OTel Resource describing this process:
// service.name/service.version from cfg, the kernel-authenticated producer
// identity when producer is non-nil, any ambient OTEL_RESOURCE_ATTRIBUTES /
// OTEL_SERVICE_NAME environment (resource.WithFromEnv — this is how the
// kernel's process-env stamp reaches a plugin subprocess's own Resource;
// see grpchooks.go's ResourceEnv), and any operator-configured extra
// resource attributes (cfg.ResourceAttrs). Later sources win over earlier
// ones on a key collision (resource.Merge's documented "b overwrites a"
// rule), in the order: SDK default < environment < explicit cfg/producer.
//
// producer is nil for the kernel process itself — the kernel has no
// plugin identity of its own. A plugin subprocess calling
// pkg/telemetry.Bootstrap passes its own identity (or nil, relying solely
// on the kernel-stamped environment).
func BuildResource(ctx context.Context, cfg Config, producer *commonv1.ProducerRef) (*resource.Resource, error) {
	envRes, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: environment: %w", err)
	}

	explicit := resource.NewSchemaless(explicitAttributes(cfg, producer)...)

	merged, err := resource.Merge(resource.Default(), envRes)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: merge environment: %w", err)
	}
	merged, err = resource.Merge(merged, explicit)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: merge explicit: %w", err)
	}
	return merged, nil
}

// explicitAttributes returns the operator/producer-controlled attribute
// set, sorted by key so BuildResource's output is stable across calls —
// helpful for tests and diffing exported resources, though (unlike
// persisted event/cost/plan rows) resource attributes are side-band
// telemetry and don't fall under determinism.md's ordering guarantee.
func explicitAttributes(cfg Config, producer *commonv1.ProducerRef) []attribute.KeyValue {
	attrs := []attribute.KeyValue{semconv.ServiceName(cfg.ServiceName)}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	attrs = append(attrs, producerAttributes(producer)...)

	keys := make([]string, 0, len(cfg.ResourceAttrs))
	for k := range cfg.ResourceAttrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		attrs = append(attrs, attribute.String(k, cfg.ResourceAttrs[k]))
	}

	return attrs
}
