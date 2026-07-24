package memory

import (
	"fmt"

	"google.golang.org/grpc/codes"

	"github.com/pluggableharness/agent/pkg/plugin"
)

// errorDomain identifies this category's error taxonomy in the
// google.rpc.ErrorInfo structured detail plugin.StatusError attaches to
// every RPC error crossing the plugin boundary
// (.claude/rules/grpc.md#error-taxonomy--codes).
const errorDomain = "memory.pluggableharness.dev"

// ErrorCategory is the structured error taxonomy every *Error classifies
// into (docs/specifications/memory/data-types.md#memoryerror,
// docs/specifications/memory/conformance.md#error-taxonomy). A Provider
// implementation MUST classify every failure into one of these and MUST
// NOT collapse them into a single generic error.
type ErrorCategory int

// The fixed MemoryErrorCategory taxonomy.
const (
	// ErrorCategoryUnspecified is never valid on a real error; its
	// presence means a caller forgot to set the field.
	ErrorCategoryUnspecified ErrorCategory = iota
	// ErrorCategoryNotFound: UpdateRecord/DeleteRecord/ApproveRecord/
	// RejectRecord/GetRecord referenced an id that doesn't exist.
	ErrorCategoryNotFound
	// ErrorCategoryInvalidType: Record specified a Type this
	// provider doesn't support.
	ErrorCategoryInvalidType
	// ErrorCategoryInvalidScope: Record specified a Scope this
	// provider doesn't support.
	ErrorCategoryInvalidScope
	// ErrorCategoryRatificationUnsupported: ApproveRecord/RejectRecord was
	// called against a provider with RatificationSupported == false.
	ErrorCategoryRatificationUnsupported
	// ErrorCategoryBudgetExceeded: Recall's candidate records exceed
	// token_budget even after this provider's own truncation.
	ErrorCategoryBudgetExceeded
	// ErrorCategorySourceUnavailable: this provider's backend storage was
	// unreachable at call time. A retry candidate.
	ErrorCategorySourceUnavailable
	// ErrorCategoryUnknown covers anything not more specifically
	// categorized above.
	ErrorCategoryUnknown
)

// String returns c's taxonomy name, e.g. "not_found".
func (c ErrorCategory) String() string {
	switch c {
	case ErrorCategoryNotFound:
		return "not_found"
	case ErrorCategoryInvalidType:
		return "invalid_type"
	case ErrorCategoryInvalidScope:
		return "invalid_scope"
	case ErrorCategoryRatificationUnsupported:
		return "ratification_unsupported"
	case ErrorCategoryBudgetExceeded:
		return "budget_exceeded"
	case ErrorCategorySourceUnavailable:
		return "source_unavailable"
	case ErrorCategoryUnknown:
		return "unknown"
	default:
		return "unspecified"
	}
}

// grpcCode maps c to the gRPC status code
// docs/specifications/memory/conformance.md#error-taxonomy mandates,
// verbatim — never codes.Unknown, per .claude/rules/grpc.md.
func (c ErrorCategory) grpcCode() codes.Code {
	switch c {
	case ErrorCategoryNotFound:
		return codes.NotFound
	case ErrorCategoryInvalidType, ErrorCategoryInvalidScope:
		return codes.InvalidArgument
	case ErrorCategoryRatificationUnsupported:
		return codes.FailedPrecondition
	case ErrorCategoryBudgetExceeded:
		return codes.ResourceExhausted
	case ErrorCategorySourceUnavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

// Error is the structured error every Provider method MUST return instead
// of an opaque error when a failure falls into one of the taxonomy
// categories above (docs/specifications/memory/data-types.md#memoryerror).
// server.go converts an *Error into the corresponding gRPC status via
// plugin.StatusError; any other error type crossing an RPC boundary is
// reported as ErrorCategoryUnknown / codes.Internal.
type Error struct {
	// Category classifies the failure.
	Category ErrorCategory
	// Message is a human-readable error detail.
	Message string
	// Retryable reports whether the kernel MAY retry this call as-is.
	Retryable bool
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("memory: %s: %s", e.Category, e.Message)
}

// grpcStatus converts e into a *status.Status-backed error via
// plugin.StatusError, carrying e.Category's gRPC code and a structured
// google.rpc.ErrorInfo detail.
func (e *Error) grpcStatus() error {
	metadata := map[string]string{"retryable": fmt.Sprintf("%t", e.Retryable)}
	return plugin.StatusError(e.Category.grpcCode(), errorDomain, e.Category.String(), e.Message, metadata)
}

// NotFound builds an *Error{Category: ErrorCategoryNotFound}, non-retryable
// — the obvious call for UpdateRecord/DeleteRecord/ApproveRecord/
// RejectRecord/GetRecord when id doesn't match an existing record
// (docs/specifications/memory/conformance.md#error-taxonomy: "Surface
// distinctly — a caller passed a stale or wrong id, not a transient
// failure").
func NotFound(message string) *Error {
	return &Error{Category: ErrorCategoryNotFound, Message: message, Retryable: false}
}

// InvalidType builds an *Error{Category: ErrorCategoryInvalidType},
// non-retryable — Record specified a Type absent from
// Capabilities.SupportedTypes.
func InvalidType(message string) *Error {
	return &Error{Category: ErrorCategoryInvalidType, Message: message, Retryable: false}
}

// InvalidScope builds an *Error{Category: ErrorCategoryInvalidScope},
// non-retryable — Record specified a Scope absent from
// Capabilities.SupportedScopes.
func InvalidScope(message string) *Error {
	return &Error{Category: ErrorCategoryInvalidScope, Message: message, Retryable: false}
}

// RatificationUnsupported builds an
// *Error{Category: ErrorCategoryRatificationUnsupported}, non-retryable —
// ApproveRecord/RejectRecord was called against a provider that doesn't
// support ratification. server.go returns this automatically when the
// provider doesn't satisfy RatificationProvider; a Provider author does
// not normally need to construct this directly.
func RatificationUnsupported(message string) *Error {
	return &Error{Category: ErrorCategoryRatificationUnsupported, Message: message, Retryable: false}
}

// BudgetExceeded builds an *Error{Category: ErrorCategoryBudgetExceeded},
// non-retryable (the caller must resubmit with a different budget, not
// retry the identical call) — Recall's candidate records still exceed
// token_budget after this provider's own truncation.
func BudgetExceeded(message string) *Error {
	return &Error{Category: ErrorCategoryBudgetExceeded, Message: message, Retryable: false}
}

// SourceUnavailable builds an
// *Error{Category: ErrorCategorySourceUnavailable}, retryable — this
// provider's backend storage was unreachable at call time
// (docs/specifications/memory/conformance.md#error-taxonomy: "Retry
// candidate — transient by nature").
func SourceUnavailable(message string) *Error {
	return &Error{Category: ErrorCategorySourceUnavailable, Message: message, Retryable: true}
}

// Unknown builds an *Error{Category: ErrorCategoryUnknown}, non-retryable
// by default — anything not covered by a more specific category above.
// message MUST include enough detail for debugging
// (docs/specifications/memory/conformance.md#error-taxonomy).
func Unknown(message string) *Error {
	return &Error{Category: ErrorCategoryUnknown, Message: message, Retryable: false}
}
