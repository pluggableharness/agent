package plugin

import (
	"context"
	"testing"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// TestResolveSupportsPreview exercises every outcome
// doc.go's "Resolving SupportsPreview" documents: a nil client, a
// successful Preview call, an Unimplemented response, any other error
// (the synthetic-arguments case), and a probe that never completes.
func TestResolveSupportsPreview(t *testing.T) {
	t.Parallel()

	producer := &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_TOOL, Name: "filesystem", Version: "1.0.0"}
	tel := testTelemetry(t)
	logger := testLogger()

	tests := []struct {
		name   string
		client *fakeToolClient
		want   bool
	}{
		{name: "success means supported", client: previewOK(), want: true},
		{name: "unimplemented means unsupported", client: previewUnimplemented(), want: false},
		{name: "any other error still means supported", client: previewInvalidArgument(), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveSupportsPreview(context.Background(), tel, logger, tt.client, producer, "read_file")
			if got != tt.want {
				t.Errorf("resolveSupportsPreview = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveSupportsPreview_nilClient confirms a nil client — a
// provider whose Live entry never asserted to a tool client — resolves
// conservatively false without attempting a call at all (previewFunc is
// never set on a nil client, so a call would nil-pointer-panic if this
// short-circuit were missing).
func TestResolveSupportsPreview_nilClient(t *testing.T) {
	t.Parallel()

	producer := &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_TOOL, Name: "filesystem", Version: "1.0.0"}
	got := resolveSupportsPreview(context.Background(), testTelemetry(t), testLogger(), nil, producer, "read_file")
	if got {
		t.Error("resolveSupportsPreview(nil client) = true, want false")
	}
}

// TestResolveSupportsPreview_timeout confirms a probe that never
// completes resolves conservatively false rather than hanging or
// panicking — exercised with an already-tight parent deadline so the
// test itself stays within the unit tier's speed budget regardless of
// previewProbeTimeout's own 5s constant (context.WithTimeout inside
// resolveSupportsPreview always honors the earlier of the two
// deadlines).
func TestResolveSupportsPreview_timeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	producer := &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_TOOL, Name: "filesystem", Version: "1.0.0"}
	got := resolveSupportsPreview(ctx, testTelemetry(t), testLogger(), previewBlocks(), producer, "read_file")
	if got {
		t.Error("resolveSupportsPreview(timeout) = true, want false")
	}
}
