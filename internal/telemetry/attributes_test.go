package telemetry

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

func TestAttributeKeys_fileAndPlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  attribute.Key
		want string
	}{
		{name: "FilePathKey", key: FilePathKey, want: "pluggableharness.agent.file.path"},
		{name: "PlatformKey", key: PlatformKey, want: "pluggableharness.agent.platform"},
		{name: "EventBusTopicKey", key: EventBusTopicKey, want: "pluggableharness.agent.eventbus.topic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(tt.key); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestProducerAttributes_nil(t *testing.T) {
	t.Parallel()

	if got := producerAttributes(nil); got != nil {
		t.Fatalf("producerAttributes(nil) = %v, want nil", got)
	}
}

func TestProducerAttributes(t *testing.T) {
	t.Parallel()

	ref := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "ripgrep",
		Version:  "1.2.3",
	}

	attrs := producerAttributes(ref)
	if len(attrs) != 3 {
		t.Fatalf("len(attrs) = %d, want 3", len(attrs))
	}

	want := map[string]string{
		string(ProducerCategoryKey): "CATEGORY_TOOL",
		string(ProducerNameKey):     "ripgrep",
		string(ProducerVersionKey):  "1.2.3",
	}
	for _, kv := range attrs {
		wantVal, ok := want[string(kv.Key)]
		if !ok {
			t.Fatalf("unexpected attribute key %q", kv.Key)
		}
		if kv.Value.AsString() != wantVal {
			t.Errorf("%s = %q, want %q", kv.Key, kv.Value.AsString(), wantVal)
		}
	}
}
