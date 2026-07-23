package producer

import (
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

func TestWithProducer_roundTrip(t *testing.T) {
	t.Parallel()

	p := &commonv1.ProducerRef{
		Category: commonv1.Category_CATEGORY_TOOL,
		Name:     "ripgrep",
		Version:  "1.2.3",
	}
	ctx := WithProducer(t.Context(), p)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext: ok = false, want true")
	}
	if got != p {
		t.Fatalf("FromContext: got %v, want the same pointer %v", got, p)
	}
}

func TestFromContext_absent(t *testing.T) {
	t.Parallel()

	got, ok := FromContext(t.Context())
	if ok {
		t.Fatalf("FromContext: ok = true, want false (no producer set)")
	}
	if got != nil {
		t.Fatalf("FromContext: got %v, want nil", got)
	}
}

func TestWithProducer_nilValue(t *testing.T) {
	t.Parallel()

	ctx := WithProducer(t.Context(), nil)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext: ok = false, want true (a nil *ProducerRef was explicitly set)")
	}
	if got != nil {
		t.Fatalf("FromContext: got %v, want nil", got)
	}
}
