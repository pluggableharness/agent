package tool

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/pluggableharness/agent/pkg/plugin"
)

// errorDomain is the domain string passed to plugin.StatusError for every
// tool-category RPC error crossing the plugin boundary, per
// .claude/rules/grpc.md's "most specific code ... category enum in
// structured detail" rule.
const errorDomain = "tool.pluggableharness.dev"

// ToolErrorCategory classifies why an Invoke call failed, per
// docs/specifications/tool/conformance.md#error-taxonomy. Deliberately
// distinct from the model category's own error taxonomy — there is no
// rate_limited or context_length_exceeded here, those are model-vendor
// concepts. One of the six types pkg/slashcommand reuses verbatim; see
// doc.go.
type ToolErrorCategory int

const (
	// ToolErrorCategoryUnspecified is the zero value. Never valid for a
	// real error.
	ToolErrorCategoryUnspecified ToolErrorCategory = iota
	// ToolErrorCategoryInvalidArguments means input failed input_schema
	// validation.
	ToolErrorCategoryInvalidArguments
	// ToolErrorCategoryNotFound means the target of the operation
	// doesn't exist (path, URL, symbol, ...).
	ToolErrorCategoryNotFound
	// ToolErrorCategoryPermissionDenied means OS/policy denied the
	// underlying operation.
	ToolErrorCategoryPermissionDenied
	// ToolErrorCategoryExecutionFailed means the operation ran but
	// failed on its own terms (non-zero exit, compiler error, HTTP
	// 4xx/5xx) — not a plugin bug.
	ToolErrorCategoryExecutionFailed
	// ToolErrorCategoryTimeout means the operation exceeded a plugin- or
	// kernel-enforced deadline.
	ToolErrorCategoryTimeout
	// ToolErrorCategoryConcurrencyConflict means the provider detected a
	// conflicting concurrent call it could not serialize itself —
	// signals the kernel to retry serialized.
	ToolErrorCategoryConcurrencyConflict
	// ToolErrorCategoryCancelled means the stream was cancelled — not
	// "an error" in the failure sense; kept distinct so the kernel
	// doesn't surface it to the model as a tool failure when the whole
	// turn is being aborted anyway.
	ToolErrorCategoryCancelled
	// toolErrorCategoryProcessCrashed is unexported and has no
	// constructor: a plugin subprocess that crashes mid-Invoke obviously
	// cannot emit this category about itself. It exists here only so
	// GRPCCode and this type's String method can render/map a category
	// value the kernel synthesizes on the transport side after a crash —
	// see NewToolError's doc comment for how this package makes it
	// unconstructable by a plugin author.
	toolErrorCategoryProcessCrashed
	// ToolErrorCategoryUnknown means anything else. Details MUST include
	// the raw underlying error.
	ToolErrorCategoryUnknown
)

// String returns c's wire-name-derived lowercase form, e.g.
// "invalid_arguments".
func (c ToolErrorCategory) String() string {
	switch c {
	case ToolErrorCategoryUnspecified:
		return "unspecified"
	case ToolErrorCategoryInvalidArguments:
		return "invalid_arguments"
	case ToolErrorCategoryNotFound:
		return "not_found"
	case ToolErrorCategoryPermissionDenied:
		return "permission_denied"
	case ToolErrorCategoryExecutionFailed:
		return "execution_failed"
	case ToolErrorCategoryTimeout:
		return "timeout"
	case ToolErrorCategoryConcurrencyConflict:
		return "concurrency_conflict"
	case ToolErrorCategoryCancelled:
		return "cancelled"
	case toolErrorCategoryProcessCrashed:
		return "process_crashed"
	case ToolErrorCategoryUnknown:
		return "unknown"
	default:
		return "unrecognized"
	}
}

// ToolError is the terminal, failed outcome of an Invoke call, per
// docs/specifications/tool/conformance.md#error-taxonomy. Implements the
// standard error interface via Error. One of the six types
// pkg/slashcommand reuses verbatim; see doc.go. Deliberately holds nothing
// ToolCall-specific so it reuses cleanly for a slash command's own
// direct-invoke failure.
type ToolError struct {
	// Category MUST be set — see ToolErrorCategory. Never
	// ToolErrorCategoryUnspecified and never the kernel-only
	// process_crashed category; see NewToolError.
	Category ToolErrorCategory
	// Message is human-readable. MUST be set.
	Message string
	// Retryable MUST be set.
	Retryable bool
	// Details is provider-specific structured detail. MUST include the
	// raw underlying error for ToolErrorCategoryUnknown.
	Details map[string]any
}

// Error implements the standard error interface, returning Message.
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Sentinel errors returned by NewToolError, checked with errors.Is.
var (
	// ErrEmptyMessage is returned when message is empty.
	ErrEmptyMessage = errors.New("tool: message must not be empty")
	// ErrUnspecifiedCategory is returned when category is
	// ToolErrorCategoryUnspecified or any value outside the declared
	// enum.
	ErrUnspecifiedCategory = errors.New("tool: category must not be unspecified")
	// ErrProcessCrashedCategory is returned when category is the
	// kernel-only process_crashed category. A plugin process that
	// crashes mid-Invoke cannot emit an event about its own crash — the
	// kernel synthesizes this category from the transport failure
	// instead — so NewToolError refuses to construct one regardless of
	// how the caller obtained the category value, closing the gap a bare
	// unexported constant alone would leave open (ToolErrorCategory is
	// just an int; a caller could still write ToolErrorCategory(8)).
	ErrProcessCrashedCategory = errors.New("tool: process_crashed is kernel-synthesized only and cannot be constructed by a plugin")
)

// NewToolError builds and validates a ToolError. It rejects an empty
// message, ToolErrorCategoryUnspecified, and the kernel-only
// process_crashed category (see ErrProcessCrashedCategory) — this is the
// package's chosen mechanism for making process_crashed unconstructable by
// a plugin author, per
// docs/specifications/tool/conformance.md#error-taxonomy: "a plugin
// process that crashes obviously cannot emit this itself."
func NewToolError(category ToolErrorCategory, message string, retryable bool, details map[string]any) (*ToolError, error) {
	if message == "" {
		return nil, fmt.Errorf("tool: new tool error: %w", ErrEmptyMessage)
	}
	if err := validateErrorCategory(category); err != nil {
		return nil, fmt.Errorf("tool: new tool error: %w", err)
	}
	return &ToolError{Category: category, Message: message, Retryable: retryable, Details: details}, nil
}

// validateErrorCategory rejects ToolErrorCategoryUnspecified, the
// kernel-only process_crashed category, and any out-of-range value; every
// other declared category is valid.
func validateErrorCategory(c ToolErrorCategory) error {
	switch c {
	case ToolErrorCategoryUnspecified:
		return ErrUnspecifiedCategory
	case toolErrorCategoryProcessCrashed:
		return ErrProcessCrashedCategory
	case ToolErrorCategoryInvalidArguments,
		ToolErrorCategoryNotFound,
		ToolErrorCategoryPermissionDenied,
		ToolErrorCategoryExecutionFailed,
		ToolErrorCategoryTimeout,
		ToolErrorCategoryConcurrencyConflict,
		ToolErrorCategoryCancelled,
		ToolErrorCategoryUnknown:
		return nil
	default:
		return fmt.Errorf("%w: %d", ErrUnspecifiedCategory, int(c))
	}
}

// GRPCCode maps a ToolErrorCategory to the grpc/codes.Code that best
// represents it when a ToolError must cross the plugin boundary as a gRPC
// status — as opposed to traveling in-band as an Invoke stream's terminal
// `error` ToolEvent (the common case, see stream.go), which never becomes
// a gRPC status at all.
//
// Two entries are judgment calls, recorded here per the task's request:
//
//   - ToolErrorCategoryExecutionFailed maps to codes.Internal.
//     execution_failed means the operation ran and failed on its own
//     terms (non-zero exit, compiler error, HTTP 4xx/5xx) rather than the
//     RPC call itself being malformed, so none of the more specific
//     argument/lookup/permission/deadline codes fit; codes.Internal is
//     .claude/rules/grpc.md's own prescribed fallback for "unmapped", and
//     execution_failed is exactly that from the transport's point of
//     view — the category exists precisely so this case is NOT surfaced
//     as a protocol-level failure in the first place (conformance.md's
//     reaction table: "Ordinary tool_result content, not a
//     protocol-level failure"), so this mapping is only ever exercised
//     on the rare path where an execution_failed ToolError has to cross
//     as a status anyway (e.g. a hand-rolled Configure-time check).
//   - ToolErrorCategoryConcurrencyConflict maps to codes.Aborted, not
//     codes.FailedPrecondition. codes.Aborted's documented meaning ("the
//     operation was aborted ... due to a concurrency issue ... the
//     client should retry at a higher level") is a closer textual match
//     than codes.FailedPrecondition's ("the client should not retry
//     until the system state has been explicitly fixed") —
//     concurrency_conflict is specifically retryable-after-serialization
//     (conformance.md), which is Aborted's documented retry semantics.
func GRPCCode(category ToolErrorCategory) codes.Code {
	switch category {
	case ToolErrorCategoryInvalidArguments:
		return codes.InvalidArgument
	case ToolErrorCategoryNotFound:
		return codes.NotFound
	case ToolErrorCategoryPermissionDenied:
		return codes.PermissionDenied
	case ToolErrorCategoryExecutionFailed:
		return codes.Internal
	case ToolErrorCategoryTimeout:
		return codes.DeadlineExceeded
	case ToolErrorCategoryConcurrencyConflict:
		return codes.Aborted
	case ToolErrorCategoryCancelled:
		return codes.Canceled
	case toolErrorCategoryProcessCrashed:
		return codes.Unavailable
	default: // Unspecified, Unknown, or any out-of-range value.
		return codes.Internal
	}
}

// ToStatusError converts te into a gRPC status error suitable for crossing
// the plugin boundary via plugin.StatusError, mapping te.Category to a
// codes.Code via GRPCCode and te.Details to string metadata. Use this for
// an error that must fail the RPC itself (Configure, GetSchema, Render,
// Preview) — never for Invoke's own result/error terminal events, which
// travel in-band as a ToolEvent (see stream.go) rather than as a gRPC
// status.
func ToStatusError(te *ToolError) error {
	if te == nil {
		return plugin.StatusError(codes.Internal, errorDomain, "nil_tool_error", "tool: nil ToolError", nil)
	}
	return plugin.StatusError(GRPCCode(te.Category), errorDomain, strings.ToLower(te.Category.String()), te.Message, detailsToMetadata(te.Details))
}

// detailsToMetadata stringifies details for plugin.StatusError's metadata
// parameter, which is typed map[string]string. Returns nil for an empty
// map so ToStatusError never attaches an empty, noise-only metadata map.
func detailsToMetadata(details map[string]any) map[string]string {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]string, len(details))
	for k, v := range details {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
