package widget

import (
	"context"
	"fmt"

	structpb "google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Capabilities is this widget provider's complete capability
// advertisement, returned from GetCapabilities. It MUST be cheap to
// compute and MUST NOT require a network call
// (docs/specifications/frontend/widget-protocol.md#transport) — a
// Provider's GetCapabilities implementation should build this from
// static, already-in-process data, never a fresh RPC or I/O call.
type Capabilities struct {
	// Regions this widget intends to contribute to. MUST be set.
	Regions []renderv1.Region
	// ConfigSchema is this provider's agent.hcl config schema — build it
	// with pkg/config.Schema and pkg/config.Attribute, or with this
	// package's NewCapabilities.
	ConfigSchema *configv1.ConfigSchema
	// SupportedHookPoints lets the kernel reject an agent.hcl hook{}
	// block naming a point this widget can't serve, at config-load time
	// rather than at first dispatch.
	SupportedHookPoints []commonv1.HookPoint
}

// AttachRequest identifies which session's widget instance a Provider's
// Attach method is being asked to serve. Per
// docs/specifications/frontend/widget-protocol.md#transport, one
// AttachRequest maps to exactly one Provider.Attach call and one
// session — never a set of sessions multiplexed on one call.
type AttachRequest struct {
	// SessionID is the session this widget instance is attaching to.
	SessionID string
}

// UpdateMode says whether an Update replaces or appends to this widget's
// prior content in its target Region. It exists specifically so the
// replace/append distinction can't be silently inverted the way a bare
// bool parameter invites — UpdateAppend is the zero value, matching the
// wire default (an unset WidgetUpdate.replace means false, i.e. append,
// per widget-protocol.md#transport).
type UpdateMode int

const (
	// UpdateAppend adds this update's Content alongside whatever this
	// widget previously pushed to the same Region, rather than replacing
	// it. This is UpdateMode's zero value.
	UpdateAppend UpdateMode = iota
	// UpdateReplace replaces this widget's prior content in the same
	// Region entirely.
	UpdateReplace
)

// String returns "append" or "replace" for the two defined UpdateMode
// values, or "unknown(N)" for any other value.
func (m UpdateMode) String() string {
	switch m {
	case UpdateAppend:
		return "append"
	case UpdateReplace:
		return "replace"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// Update is one pushed update to this widget's rendered content for one
// session, per docs/specifications/frontend/widget-protocol.md#transport
// (the wire message is WidgetUpdate; this is its domain-side
// representation). Mode governs whether Content replaces or appends to
// this widget's prior content in Region — see UpdateMode's doc comment
// for why that distinction is a named type rather than a bare bool.
type Update struct {
	// Region this update places Content into.
	Region renderv1.Region
	// Content to place — build it with pkg/render.
	Content *renderv1.RenderTree
	// Mode says whether this update replaces or appends to this widget's
	// prior content in Region.
	Mode UpdateMode
}

// Provider is the author-facing interface a widget plugin implements. A
// concrete Provider is handed to NewService, which adapts it to
// widgetv1.WidgetServiceServer.
type Provider interface {
	// GetCapabilities returns this widget's regions, config schema, and
	// supported hook points. MUST be cheap and MUST NOT make a network
	// call (docs/specifications/frontend/widget-protocol.md#transport).
	GetCapabilities(ctx context.Context) (Capabilities, error)
	// Configure decodes and validates this provider's agent.hcl block,
	// already-decoded to config. Return an *Error (or any error) to
	// reject it; Service surfaces it as a gRPC status, never echoing a
	// received secret back out.
	Configure(ctx context.Context, config *structpb.Struct) error
	// Attach serves one session's update feed for as long as the kernel
	// keeps the stream open, pushing every update through sender.Send.
	// ctx is canceled when the kernel closes the stream — ordinary
	// control flow, not a failure. Attach SHOULD return promptly once ctx
	// is done (returning ctx.Err() is the idiomatic choice) rather than
	// treating cancellation as an error condition worth reporting.
	Attach(ctx context.Context, req AttachRequest, sender *UpdateSender) error
}
