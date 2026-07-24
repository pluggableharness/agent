package frontend

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// errorDomain is the google.rpc.ErrorInfo domain passed to
// plugin.StatusError for every Error this package surfaces as a
// gRPC status, per .claude/rules/grpc.md's error taxonomy.
const errorDomain = "frontend.pluggableharness.dev"

// Error is the domain form of frontendv1.FrontendError — the
// structured error type for this category, carried in ServerEvent.error
// mid-Attach and in the structured detail of a Configure-time gRPC status
// (doc.go's "Error handling is two distinct paths, not one").
//
// The ten FrontendErrorCategory values, and this package's mapping to
// grpc/codes.Code (used only for the Configure-time gRPC-status path;
// mid-Attach errors carry the category in-band and never touch a
// grpc/codes.Code at all):
//
//	FRONTEND_ERROR_CATEGORY_UNSPECIFIED             codes.Internal        (never a valid category to send; treated as an internal bug)
//	FRONTEND_ERROR_CATEGORY_RENDER_FAILED           codes.Internal        (a RenderTree/PlacedContent could not be painted; reported in-band in the ordinary case, never expected at Configure time)
//	FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT    codes.InvalidArgument (malformed input on the operator-facing side)
//	FRONTEND_ERROR_CATEGORY_REGION_UNSUPPORTED      codes.FailedPrecondition (this frontend has no fallback behavior at all for the targeted Region)
//	FRONTEND_ERROR_CATEGORY_UNKNOWN                 codes.Internal        (anything else; never codes.Unknown, per .claude/rules/grpc.md)
//	FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND       codes.NotFound        (attach/resume/detach/list named a session_id the kernel has no record of)
//	FRONTEND_ERROR_CATEGORY_SESSION_CREATE_FAILED   codes.InvalidArgument (create_session failed: an invalid profile or unusable working_directory)
//	FRONTEND_ERROR_CATEGORY_SESSION_BUSY            codes.FailedPrecondition (reserved; no variant in this protocol revision currently triggers it)
//	FRONTEND_ERROR_CATEGORY_SCHEMA_TOO_NEW          codes.FailedPrecondition (resume_session named a session file newer than this kernel understands)
//	FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY     codes.FailedPrecondition (a new-turn-inducing event targeted a session attached replay-only)
type Error struct {
	Category frontendv1.FrontendErrorCategory
	Message  string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("frontend: %s: %s", e.Category, e.Message)
}

// grpcCode maps e.Category to the grpc/codes.Code a Configure-time status
// built from e carries, per the table on Error's own doc comment.
func (e *Error) grpcCode() codes.Code {
	switch e.Category {
	case frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_CREATE_FAILED:
		return codes.InvalidArgument
	case frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND:
		return codes.NotFound
	case frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_REGION_UNSUPPORTED,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_BUSY,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SCHEMA_TOO_NEW,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY:
		return codes.FailedPrecondition
	default:
		// FRONTEND_ERROR_CATEGORY_UNSPECIFIED, FRONTEND_ERROR_CATEGORY_UNKNOWN,
		// FRONTEND_ERROR_CATEGORY_RENDER_FAILED, and any future category this
		// build predates: codes.Internal, never codes.Unknown
		// (.claude/rules/grpc.md's error taxonomy).
		return codes.Internal
	}
}

// StatusErr builds the gRPC status a Configure-time e surfaces as, per
// frontend-protocol.md's "ConfigureResponse errors surface as a gRPC
// status carrying a Error in its structured detail."
func (e *Error) StatusErr() error {
	return plugin.StatusError(e.grpcCode(), errorDomain, e.Category.String(), e.Message, nil)
}

// statusErr converts an arbitrary error returned by Provider.Capabilities
// or Provider.Configure into the gRPC status NewService's unary handlers
// return: err's own Error when it carries one, or a generic
// FRONTEND_ERROR_CATEGORY_UNKNOWN status otherwise.
func statusErr(err error) error {
	var fe *Error
	if errors.As(err, &fe) {
		return fe.StatusErr()
	}
	return (&Error{
		Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNKNOWN,
		Message:  err.Error(),
	}).StatusErr()
}

// inBandError converts an arbitrary error returned by Provider.HandleEvent
// into the Error attach.go reports in-band via ErrorEvent: err's
// own Error when it carries one, or a generic
// FRONTEND_ERROR_CATEGORY_UNKNOWN otherwise.
func inBandError(err error) *Error {
	var fe *Error
	if errors.As(err, &fe) {
		return fe
	}
	return &Error{
		Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNKNOWN,
		Message:  err.Error(),
	}
}

// FatalErr signals, when returned by Fatal, that attach.go's dispatch loop
// should treat Err as a genuinely fatal condition — the plugin process
// itself failing — so the Attach stream MUST close with a gRPC status
// rather than take the ordinary in-band ServerEvent.error path every other
// Provider.HandleEvent error takes (doc.go's "Error handling is two
// distinct paths, not one"; frontend-protocol.md's error-taxonomy
// asymmetry).
type FatalErr struct {
	Err error
}

// Fatal wraps err so attach.go's dispatch loop closes the Attach stream
// with a gRPC status instead of reporting err in-band. Deliberately named
// and shaped differently from an ordinary returned error, so closing the
// long-lived stream requires an author's own deliberate choice rather than
// happening by accident. Fatal(nil) returns nil.
func Fatal(err error) error {
	if err == nil {
		return nil
	}
	return &FatalErr{Err: err}
}

// Error implements the error interface.
func (f *FatalErr) Error() string {
	return "frontend: fatal: " + f.Err.Error()
}

// Unwrap supports errors.Is/errors.As against the wrapped error.
func (f *FatalErr) Unwrap() error {
	return f.Err
}
