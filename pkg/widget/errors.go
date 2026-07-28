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
// status carries, per plugin.StatusError's convention.
const errorDomain = "widget.pluggableharness.dev"

const (
	reasonRenderFailed = "render_failed"
	reasonUnknown      = "unknown"
)

// metadataCategoryKey names the google.rpc.ErrorInfo metadata entry
// carrying WidgetErrorCategory's exact wire enum name.
const metadataCategoryKey = "category"

// Error is the widget category's structured error type — the domain-side
// representation of the wire WidgetError message. A Service always carries
// it in the structured detail of a gRPC status returned from Configure,
// never as an in-band stream message.
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

// RenderFailed builds an Error reporting that a render or metadata
// contribution could not be produced. Maps to codes.Internal.
func RenderFailed(message string) *Error {
	return &Error{Category: widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_RENDER_FAILED, Message: message}
}

// Unknown builds an Error for a failure that fits none of the other
// categories. Maps to codes.Internal, never codes.Unknown.
func Unknown(message string) *Error {
	return &Error{Category: widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_UNKNOWN, Message: message}
}

// widgetGRPCCode is the codes.Code every currently-defined widget error
// category maps to. Both RENDER_FAILED and UNKNOWN are Internal per
// conformance.md's canonical table — never codes.Unknown. This is a
// constant rather than a switch precisely because a switch whose arms all
// return the same value reads as a mapping that exists when it does not; if
// a future category maps elsewhere (InvalidArgument, Canceled), reintroduce
// the switch then, with arms that actually differ.
const widgetGRPCCode = codes.Internal

// reason returns e.Category's reason string.
func (e *Error) reason() string {
	switch e.Category {
	case widgetv1.WidgetErrorCategory_WIDGET_ERROR_CATEGORY_RENDER_FAILED:
		return reasonRenderFailed
	default:
		return reasonUnknown
	}
}

// toStatus builds the gRPC status a Service returns for e.
func (e *Error) toStatus() error {
	return plugin.StatusError(widgetGRPCCode, errorDomain, e.reason(), e.Message, map[string]string{
		metadataCategoryKey: e.Category.String(),
	})
}

// FromStatus recovers an *Error from err if err is a gRPC status carrying
// this package's ErrorInfo detail.
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
