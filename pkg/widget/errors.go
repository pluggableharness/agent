package widget

import (
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/plugin"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// errorDomain is the google.rpc.ErrorInfo domain every Error-backed gRPC
// status carries, per plugin.StatusError's own "domain should be the
// calling category's own error-taxonomy name" convention.
const errorDomain = "widget.pluggableharness.dev"

// Reason strings match
// docs/specifications/frontend/widget-protocol.md#error-taxonomy's
// category names exactly, so a caller comparing against the spec's own
// vocabulary doesn't have to translate an enum name.
const (
	reasonRenderFailed      = "render_failed"
	reasonRegionUnsupported = "region_unsupported"
	reasonUnknown           = "unknown"
)

// metadataCategoryKey names the google.rpc.ErrorInfo metadata entry
// carrying WidgetErrorCategory's exact wire enum name (e.g.
// "WIDGET_ERROR_CATEGORY_RENDER_FAILED"), alongside the coarser reason
// string, so FromStatus can recover the precise category instead of
// re-deriving it from reason.
const metadataCategoryKey = "category"

// Error is the widget category's structured error type — the domain-side
// representation of the wire WidgetError message
// (docs/specifications/frontend/widget-protocol.md#error-taxonomy).
// Unlike the frontend category's FrontendError, Error has no in-band wire
// representation on this package's server-streaming-only Attach — a
// Service always carries it in the structured detail of a gRPC status
// returned from Configure or Attach (see toStatus), never as an Update
// field. Construct one with RenderFailed, RegionUnsupported, or Unknown,
// or return any other error from a Provider method and let Service map it
// to WIDGET_ERROR_CATEGORY_UNKNOWN automatically.
type Error struct {
	// Category classifies this error.
	Category widgetv1.WidgetErrorCategory
	// Message is a human-readable description.
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("widget: %s: %s", e.Category, e.Message)
}

// RenderFailed builds an Error reporting that a RenderTree or Update
// could not be produced. Maps to codes.Internal — a render-time failure
// isn't the caller's fault, and there is no more specific code for "this
// widget's rendering logic broke."
func RenderFailed(message string) *Error {
	return &Error{Category: widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_RENDER_FAILED, Message: message}
}

// RegionUnsupported builds an Error reporting that this widget was asked
// to produce a render it structurally can't — including a
// partial-failure update that renders for one Region but not another
// (docs/specifications/frontend/widget-protocol.md#error-taxonomy). Maps
// to codes.InvalidArgument, per widget-protocol.md#error-taxonomy's
// explicit "codes.InvalidArgument for ... a render this widget can't
// produce."
func RegionUnsupported(message string) *Error {
	return &Error{Category: widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_REGION_UNSUPPORTED, Message: message}
}

// Unknown builds an Error for a failure that fits none of the other
// categories. Maps to codes.Internal, never codes.Unknown — the same
// "most specific code" discipline .claude/rules/grpc.md requires
// everywhere else in this protocol series. Service.toGRPCStatus also
// falls back to this constructor automatically for any Provider error
// that isn't already an *Error.
func Unknown(message string) *Error {
	return &Error{Category: widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_UNKNOWN, Message: message}
}

// grpcCode returns the codes.Code e maps to, per
// docs/specifications/frontend/widget-protocol.md#error-taxonomy.
func (e *Error) grpcCode() codes.Code {
	switch e.Category {
	case widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_RENDER_FAILED:
		return codes.Internal
	case widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_REGION_UNSUPPORTED:
		return codes.InvalidArgument
	default:
		// WIDGET_ERROR_CATEGORY_UNKNOWN, and WIDGET_ERROR_CATEGORY_UNSPECIFIED
		// (a hand-built Error that skipped RenderFailed/
		// RegionUnsupported/Unknown), both fall through to codes.Internal.
		return codes.Internal
	}
}

// reason returns e.Category's spec-vocabulary reason string.
func (e *Error) reason() string {
	switch e.Category {
	case widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_RENDER_FAILED:
		return reasonRenderFailed
	case widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_REGION_UNSUPPORTED:
		return reasonRegionUnsupported
	default:
		return reasonUnknown
	}
}

// toStatus builds the gRPC status a Service returns for e, per
// docs/specifications/frontend/widget-protocol.md#error-taxonomy: e is
// always carried in the structured detail of a gRPC status returned from
// Configure or Attach, never as an in-band Update field.
func (e *Error) toStatus() error {
	return plugin.StatusError(e.grpcCode(), errorDomain, e.reason(), e.Message, map[string]string{
		metadataCategoryKey: e.Category.String(),
	})
}

// FromStatus recovers an *Error from err if err is a gRPC status carrying
// this package's ErrorInfo detail (i.e. one built by toStatus, reached by
// a Provider's Configure or Attach method returning an error) — the
// Error-aware counterpart to errors.As, for a caller that received this
// error across the plugin boundary rather than constructed it locally. ok
// is false for any other error, including a plain codes.Canceled
// cancellation status, which
// docs/specifications/frontend/widget-protocol.md#error-taxonomy treats
// as never an application error in the first place.
func FromStatus(err error) (*Error, bool) {
	st, ok := status.FromError(err)
	if !ok {
		return nil, false
	}
	for _, d := range st.Details() {
		info, ok := d.(*errdetails.ErrorInfo)
		if !ok || info.GetDomain() != errorDomain {
			continue
		}
		category := widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_UNKNOWN
		if n, ok := widgetv1.WidgetErrorCategory_value[info.GetMetadata()[metadataCategoryKey]]; ok {
			category = widgetv1.WidgetErrorCategory(n)
		}
		return &Error{Category: category, Message: st.Message()}, true
	}
	return nil, false
}
