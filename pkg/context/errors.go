package context

import (
	"context"
	"errors"
	"strconv"

	"google.golang.org/grpc/codes"

	"github.com/pluggableharness/agent/pkg/plugin"
)

// ErrorCategory classifies a context provider's failures
// (conformance.md#error-taxonomy). A plugin MUST classify every failure
// into one of these rather than collapsing them into one generic error.
type ErrorCategory int32

const (
	// ErrorCategoryUnspecified is the zero value. Never valid on an
	// error a provider actually returns.
	ErrorCategoryUnspecified ErrorCategory = iota
	// ErrorCategorySourceUnavailable means a declared file/glob/source was
	// unreadable at call time. Kernel reaction: drop the section for
	// this turn, log; do not fail the turn.
	ErrorCategorySourceUnavailable
	// ErrorCategoryBudgetExceeded means this provider's own section (or, for
	// a compactor, its whole returned chain) exceeds token_budget.
	// Kernel reaction: reject the section; do not fail the turn for a
	// non-compactor violator.
	ErrorCategoryBudgetExceeded
	// ErrorCategoryScopeViolation means a non-compactor provider mutated a
	// section it doesn't own. Kernel reaction: discard the entire
	// response, restore the prior chain, log.
	ErrorCategoryScopeViolation
	// ErrorCategoryInvalidRequest means a malformed request — a kernel/adapter
	// bug. MUST NOT be retried as-is.
	ErrorCategoryInvalidRequest
	// ErrorCategoryUnknown covers anything else. The message MUST include the
	// raw plugin error message for debugging.
	ErrorCategoryUnknown
)

// reason returns the spec's own lowercase-snake vocabulary name for c
// (conformance.md#error-taxonomy's table), used as the structured
// google.rpc.ErrorInfo.Reason plugin.StatusError attaches.
func (c ErrorCategory) reason() string {
	switch c {
	case ErrorCategorySourceUnavailable:
		return "source_unavailable"
	case ErrorCategoryBudgetExceeded:
		return "budget_exceeded"
	case ErrorCategoryScopeViolation:
		return "scope_violation"
	case ErrorCategoryInvalidRequest:
		return "invalid_request"
	case ErrorCategoryUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// code returns the canonical grpc/codes.Code c maps to
// (conformance.md#error-taxonomy's wire mapping table). unknown maps to
// codes.Internal, never codes.Unknown.
func (c ErrorCategory) code() codes.Code {
	switch c {
	case ErrorCategorySourceUnavailable:
		return codes.Unavailable
	case ErrorCategoryBudgetExceeded:
		return codes.FailedPrecondition
	case ErrorCategoryScopeViolation:
		return codes.PermissionDenied
	case ErrorCategoryInvalidRequest:
		return codes.InvalidArgument
	default:
		return codes.Internal
	}
}

// Error is the structured error a Provider method returns to
// classify a failure per conformance.md#error-taxonomy. Category and
// Message MUST be set; Retryable MUST be an honest signal — the kernel
// may use it to decide whether to retry the call that produced this
// error.
type Error struct {
	Category  ErrorCategory
	Message   string
	Retryable bool
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Message
}

// errorDomain is the google.rpc.ErrorInfo.Domain every Error this
// package translates carries, per .claude/rules/grpc.md's "domain is the
// calling category's own error-taxonomy name" convention.
const errorDomain = "context.pluggableharness.dev"

// toStatusError converts an error returned from a Provider method into a
// gRPC status error suitable for an RPC handler to return. Cancellation
// (context.Canceled / context.DeadlineExceeded) is normal control flow,
// never an application error (.claude/rules/grpc.md), and is mapped to
// its matching gRPC code directly rather than through the Error
// taxonomy. A *Error is mapped via its own Category; any other
// error is treated as ErrorCategoryUnknown (codes.Internal, never
// codes.Unknown), with the original error's message preserved for
// debugging per conformance.md's "unknown" row.
func toStatusError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, context.Canceled):
		return plugin.StatusError(codes.Canceled, errorDomain, "canceled", err.Error(), nil)
	case errors.Is(err, context.DeadlineExceeded):
		return plugin.StatusError(codes.DeadlineExceeded, errorDomain, "deadline_exceeded", err.Error(), nil)
	}

	var ctxErr *Error
	if errors.As(err, &ctxErr) {
		return plugin.StatusError(ctxErr.Category.code(), errorDomain, ctxErr.Category.reason(), ctxErr.Message, map[string]string{
			"retryable": strconv.FormatBool(ctxErr.Retryable),
		})
	}

	return plugin.StatusError(ErrorCategoryUnknown.code(), errorDomain, ErrorCategoryUnknown.reason(), err.Error(), nil)
}
