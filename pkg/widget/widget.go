package widget

import (
	"context"

	structpb "google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// Capabilities is this widget provider's complete capability
// advertisement, returned from GetCapabilities. It MUST be cheap to
// compute and MUST NOT require a network call.
type Capabilities struct {
	// ConfigSchema is this provider's agent.hcl config schema — build it
	// with pkg/config.Schema and pkg/config.Attribute, or with this
	// package's NewCapabilities.
	ConfigSchema *configv1.ConfigSchema
	// SupportedHookPoints lets the kernel reject an agent.hcl hook{}
	// block naming a point this widget can't serve, at config-load time
	// rather than at first dispatch.
	SupportedHookPoints []commonv1.HookPoint
}

// Provider is the author-facing interface a widget plugin implements. A
// concrete Provider is handed to NewService, which adapts it to
// widgetv1.WidgetServiceServer.
//
// There is no Attach stream. A widget that wants screen presence calls
// KernelCallbackService.PublishMetadata on the callback channel — the
// same path a tool provider uses for a status block.
type Provider interface {
	// GetCapabilities returns this widget's config schema and supported
	// hook points. MUST be cheap and MUST NOT make a network call.
	GetCapabilities(ctx context.Context) (Capabilities, error)
	// Configure decodes and validates this provider's agent.hcl block.
	// Return an *Error (or any error) to reject it; Service surfaces it
	// as a gRPC status, never echoing a received secret back out.
	Configure(ctx context.Context, config *structpb.Struct) error
}
