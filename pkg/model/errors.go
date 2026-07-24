package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// errorDomain is the google.rpc.ErrorInfo domain every *Error's
// StatusError carries, per .claude/rules/grpc.md's
// "most specific code, category enum in structured detail" convention.
const errorDomain = "model.pluggableharness.dev"

// ErrStreamAlreadyTerminated is returned by every Sink method once a
// terminal event (Stop or Error) has already been sent — at most one
// terminal event may close a StreamCompletion stream, per
// docs/specifications/model/data-types.md#streamevent.
var ErrStreamAlreadyTerminated = errors.New("model: stream already terminated")

// ErrInvalidCapabilities is wrapped by NewCapabilities when the assembled
// Capabilities value violates a MUST-level invariant from
// docs/specifications/model/data-types.md#modelspec.
var ErrInvalidCapabilities = errors.New("model: invalid capabilities")

// ErrInvalidPricing is wrapped by validatePricing when a Spec's
// Pricing violates docs/specifications/model/data-types.md#pricing's
// tier-matching invariant.
var ErrInvalidPricing = errors.New("model: invalid pricing")

// Error is the domain shape of the structured error taxonomy every
// StreamCompletion/Configure failure MUST classify into, per
// docs/specifications/model/conformance.md#error-taxonomy. A Provider
// returns an *Error (or a wrapped one, checked with errors.As) from
// Configure/StreamCompletion, or passes one to Sink.Error for an in-band
// stream failure; server.go converts it to the matching codes.Code via
// pkg/plugin.StatusError in both cases.
type Error struct {
	// Category classifies this failure. MUST be set to something other
	// than MODEL_ERROR_CATEGORY_UNSPECIFIED.
	Category modelv1.ModelErrorCategory
	// Message is a human-readable description of the failure.
	Message string
	// Retryable reports whether the kernel may retry this request as-is.
	Retryable bool
	// RetryAfter is how long the kernel should wait before retrying, when
	// the vendor supplies one (typically alongside
	// MODEL_ERROR_CATEGORY_RATE_LIMITED). SHOULD be set when available;
	// zero means unset.
	RetryAfter time.Duration
	// RawDetail is the raw vendor-provided error code or body, for
	// debugging. SHOULD be set; empty means unset.
	RawDetail string
}

// Error implements error.
func (e *Error) Error() string {
	return fmt.Sprintf("model: %s: %s", categoryReason(e.Category), e.Message)
}

// code maps e.Category to a grpc/codes.Code, per
// docs/specifications/model/conformance.md#error-taxonomy's wire-mapping
// table.
func (e *Error) code() codes.Code {
	switch e.Category {
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED:
		return codes.ResourceExhausted
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED:
		return codes.ResourceExhausted
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED:
		return codes.Unavailable
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR:
		return codes.Unauthenticated
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST:
		return codes.InvalidArgument
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED:
		return codes.FailedPrecondition
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN,
		modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNSPECIFIED:
		return codes.Internal
	default:
		// Never codes.Unknown, per conformance.md's error taxonomy table —
		// an unmapped category is Internal, same as UNKNOWN/UNSPECIFIED.
		return codes.Internal
	}
}

// categoryReason returns the lower_snake reason string StatusError's
// google.rpc.ErrorInfo.Reason carries for category, derived from the
// generated enum's own string representation.
func categoryReason(category modelv1.ModelErrorCategory) string {
	name, ok := modelv1.ModelErrorCategory_name[int32(category)]
	if !ok {
		name = modelv1.ModelErrorCategory_name[int32(modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN)]
	}
	return name
}

// StatusError converts e into a gRPC status error via pkg/plugin.StatusError
// — the canonical shape every RPC-boundary error in this package uses.
// retryable, retry_after_seconds (when set), and raw_detail (when set) are
// carried as structured metadata.
func (e *Error) StatusError() error {
	metadata := map[string]string{
		"retryable": strconv.FormatBool(e.Retryable),
	}
	if e.RetryAfter > 0 {
		metadata["retry_after_seconds"] = strconv.FormatFloat(e.RetryAfter.Seconds(), 'f', -1, 64)
	}
	if e.RawDetail != "" {
		metadata["raw_detail"] = e.RawDetail
	}
	return plugin.StatusError(e.code(), errorDomain, categoryReason(e.Category), e.Message, metadata)
}

// toProto converts e into the wire modelv1.ModelError carried by an in-band
// StreamEvent Error variant (docs/specifications/model/data-types.md#streamevent).
func (e *Error) toProto() *modelv1.ModelError {
	out := &modelv1.ModelError{
		Category:  e.Category,
		Message:   e.Message,
		Retryable: e.Retryable,
	}
	if e.RetryAfter > 0 {
		out.RetryAfter = durationpb.New(e.RetryAfter)
	}
	if e.RawDetail != "" {
		rawDetail := e.RawDetail
		out.RawDetail = &rawDetail
	}
	return out
}

// modelErrorFromProto is toProto's inverse, used by convert_test.go to
// round-trip *Error <-> *modelv1.ModelError.
func modelErrorFromProto(in *modelv1.ModelError) *Error {
	if in == nil {
		return nil
	}
	out := &Error{
		Category:  in.GetCategory(),
		Message:   in.GetMessage(),
		Retryable: in.GetRetryable(),
		RawDetail: in.GetRawDetail(),
	}
	if d := in.GetRetryAfter(); d != nil {
		out.RetryAfter = d.AsDuration()
	}
	return out
}

// statusFromErr converts any error returned by a Provider (or produced
// internally by server.go) into the gRPC status error crossing the plugin
// boundary. Cancellation is checked first and always maps to a bare
// codes.Canceled status — never an application error, per
// docs/specifications/model/README.md#transport--lifecycle and
// .claude/rules/grpc.md's cancellation rule — regardless of whether err
// also happens to satisfy errors.As against *Error. An *Error
// (found via errors.As, so a wrapped one is still recognized) converts via
// its own StatusError; anything else is unmapped and becomes
// codes.Internal, never codes.Unknown, per
// docs/specifications/model/conformance.md#error-taxonomy.
func statusFromErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return status.Error(codes.Canceled, "model: request cancelled")
	}
	var modelErr *Error
	if errors.As(err, &modelErr) {
		return modelErr.StatusError()
	}
	return plugin.StatusError(codes.Internal, errorDomain, "internal", err.Error(), nil)
}
