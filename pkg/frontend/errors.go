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

// Error is the domain form of frontendv1.FrontendError — the structured
// error type for this category, carried in the structured detail of a
// gRPC status (Configure, and residual frontend-local failures).
//
//	FRONTEND_ERROR_CATEGORY_UNSPECIFIED           codes.Internal
//	FRONTEND_ERROR_CATEGORY_RENDER_FAILED         codes.Internal
//	FRONTEND_ERROR_CATEGORY_INVALID_REQUEST       codes.InvalidArgument
//	FRONTEND_ERROR_CATEGORY_UNKNOWN               codes.Internal
//	FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND     codes.NotFound
//	FRONTEND_ERROR_CATEGORY_SESSION_CREATE_FAILED codes.InvalidArgument
//	FRONTEND_ERROR_CATEGORY_SESSION_BUSY          codes.FailedPrecondition
//	FRONTEND_ERROR_CATEGORY_SCHEMA_TOO_NEW        codes.FailedPrecondition
//	FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY   codes.FailedPrecondition
type Error struct {
	Category frontendv1.FrontendErrorCategory
	Message  string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("frontend: %s: %s", e.Category, e.Message)
}

// grpcCode maps e.Category to the grpc/codes.Code a status built from e
// carries, per the table on Error's own doc comment.
func (e *Error) grpcCode() codes.Code {
	switch e.Category {
	case frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_INVALID_REQUEST,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_CREATE_FAILED:
		return codes.InvalidArgument
	case frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND:
		return codes.NotFound
	case frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_BUSY,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SCHEMA_TOO_NEW,
		frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY:
		return codes.FailedPrecondition
	default:
		return codes.Internal
	}
}

// StatusErr builds the gRPC status e surfaces as.
func (e *Error) StatusErr() error {
	return plugin.StatusError(e.grpcCode(), errorDomain, e.Category.String(), e.Message, nil)
}

// statusErr converts an arbitrary error returned by Provider.Capabilities
// or Provider.Configure into the gRPC status NewService's unary handlers
// return: err's own Error when it carries one, or a generic
// FRONTEND_ERROR_CATEGORY_UNKNOWN wrapper otherwise.
func statusErr(err error) error {
	if err == nil {
		return nil
	}
	var fe *Error
	if errors.As(err, &fe) {
		return fe.StatusErr()
	}
	return (&Error{
		Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNKNOWN,
		Message:  err.Error(),
	}).StatusErr()
}
