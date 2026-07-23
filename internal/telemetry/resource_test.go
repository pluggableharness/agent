package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/pluggableharness/agent/internal/telemetry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

func TestBuildResource_serviceName(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test-kernel"
	cfg.ServiceVersion = "0.0.1"

	res, err := telemetry.BuildResource(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	assertAttr(t, res.Attributes(), semconv.ServiceNameKey, "test-kernel")
	assertAttr(t, res.Attributes(), semconv.ServiceVersionKey, "0.0.1")

	if _, ok := res.Set().Value(telemetry.ProducerCategoryKey); ok {
		t.Error("resource has a producer.category attribute with a nil producer")
	}
}

func TestBuildResource_producer(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test-plugin"

	producer := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "ripgrep",
		Version:  "1.2.3",
	}

	res, err := telemetry.BuildResource(context.Background(), cfg, producer)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	assertAttr(t, res.Attributes(), telemetry.ProducerCategoryKey, "CATEGORY_TOOL")
	assertAttr(t, res.Attributes(), telemetry.ProducerNameKey, "ripgrep")
	assertAttr(t, res.Attributes(), telemetry.ProducerVersionKey, "1.2.3")
}

func TestBuildResource_resourceAttrs(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test-kernel"
	cfg.ResourceAttrs = map[string]string{"env": "dev", "region": "us-east"}

	res, err := telemetry.BuildResource(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	assertAttr(t, res.Attributes(), attribute.Key("env"), "dev")
	assertAttr(t, res.Attributes(), attribute.Key("region"), "us-east")
}

func TestBuildResource_deterministic(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "test-kernel"
	cfg.ResourceAttrs = map[string]string{"env": "dev", "region": "us-east", "zone": "a"}

	res1, err := telemetry.BuildResource(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	res2, err := telemetry.BuildResource(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	if !res1.Equal(res2) {
		t.Errorf("BuildResource is not deterministic across identical calls:\n%s\n!=\n%s", res1, res2)
	}
}

func assertAttr(t *testing.T, attrs []attribute.KeyValue, key attribute.Key, want string) {
	t.Helper()
	for _, kv := range attrs {
		if kv.Key == key {
			if kv.Value.AsString() != want {
				t.Errorf("%s = %q, want %q", key, kv.Value.AsString(), want)
			}
			return
		}
	}
	t.Errorf("attribute %s not found", key)
}
