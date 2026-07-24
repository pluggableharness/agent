package hook

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// errorDomain is this package's google.rpc.ErrorInfo domain, per
// .claude/rules/grpc.md's "most specific code, category enum in
// structured detail" convention.
const errorDomain = "hook.pluggableharness.dev"

// ErrSubscriberNotImplemented is wrapped into the gRPC status
// errNotImplemented returns when a DispatchHook request's mode names a
// facet (Observer, Transformer, Vetoer) the constructed Service's
// subscriber value does not implement — an agent.hcl/plugin mismatch, not
// a malformed subscriber response.
var ErrSubscriberNotImplemented = errors.New("hook: subscriber does not implement the requested mode")

// errorMetadata builds the google.rpc.ErrorInfo metadata this package
// attaches to every structured error, so a kernel inspecting the status
// detail can reconstruct enough of a HookError
// (agent-loop/hook-dispatch.md; errors.pb.go's HookError doc comment)
// without re-deriving it purely from the gRPC code.
func errorMetadata(point commonv1.HookPoint, mode hookv1.HookMode, category hookv1.HookErrorCategory) map[string]string {
	return map[string]string{
		"hook_point": point.String(),
		"mode":       mode.String(),
		"category":   category.String(),
	}
}

// errInvalidResponse builds the HOOK_ERROR_CATEGORY_INVALID_RESPONSE
// error for every shape-mismatch case
// agent-loop/hook-dispatch.md#invalid_response-handling defines: a
// transform response of the wrong oneof variant, a transform response
// mutating a field the mutable-field table doesn't list, or
// HOOK_DECISION_UNSPECIFIED on a veto response. codes.InvalidArgument
// because the failure is the subscriber's own response, not a transport
// or downstream condition — per rpc_response.pb.go's VetoResult doc
// comment, the fail-closed conversion for a veto response this invalid is
// the kernel's job once it sees this status, not this package fabricating
// an in-band VetoResult{DENY} here.
func errInvalidResponse(point commonv1.HookPoint, mode hookv1.HookMode, reason, message string) error {
	return plugin.StatusError(codes.InvalidArgument, errorDomain, reason, message,
		errorMetadata(point, mode, hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_INVALID_RESPONSE))
}

// errTransformFailed builds the HOOK_ERROR_CATEGORY_TRANSFORM_FAILED error
// for a Transformer that returned a genuine error (as opposed to an
// invalid response shape, which is errInvalidResponse's job).
// codes.Internal per .claude/rules/grpc.md's "Internal is the safe
// unmapped default, never Unknown" for a subscriber-side failure with no
// more specific taxonomy entry.
func errTransformFailed(point commonv1.HookPoint, cause error) error {
	return plugin.StatusError(codes.Internal, errorDomain, "transform_failed", fmt.Sprintf("hook: transform: %v", cause),
		errorMetadata(point, hookv1.HookMode_HOOK_MODE_TRANSFORM, hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_TRANSFORM_FAILED))
}

// errVetoFailed builds the HOOK_ERROR_CATEGORY_VETO_FAILED error for a
// Vetoer that returned a genuine error. The kernel treats this identically
// to an explicit deny — fail-closed
// (agent-loop/hook-dispatch.md#subscriber-error-handling) — by virtue of
// this being a non-nil gRPC error at all, not because this package encodes
// DecisionDeny anywhere in it.
func errVetoFailed(point commonv1.HookPoint, cause error) error {
	return plugin.StatusError(codes.Internal, errorDomain, "veto_failed", fmt.Sprintf("hook: veto: %v", cause),
		errorMetadata(point, hookv1.HookMode_HOOK_MODE_VETO, hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_VETO_FAILED))
}

// errObserveFailed builds the error for an Observer that returned a
// genuine error. HookErrorCategory has no dedicated
// HOOK_ERROR_CATEGORY_OBSERVE_FAILED entry (only transform and veto get
// named categories — errors.pb.go), so this uses
// HOOK_ERROR_CATEGORY_UNKNOWN; codes.Internal for the same
// safe-unmapped-default reason as errTransformFailed/errVetoFailed. This
// error still reaches the kernel as a normal RPC failure — see
// server.go's DispatchHook doc comment for why this layer reports rather
// than swallows an Observer error.
func errObserveFailed(point commonv1.HookPoint, cause error) error {
	return plugin.StatusError(codes.Internal, errorDomain, "observe_failed", fmt.Sprintf("hook: observe: %v", cause),
		errorMetadata(point, hookv1.HookMode_HOOK_MODE_OBSERVE, hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN))
}

// errNotImplemented builds the error DispatchHook returns when the
// constructed Service's subscriber doesn't implement the facet mode
// requires — wrapping ErrSubscriberNotImplemented. codes.Unimplemented:
// this is a plugin/agent.hcl configuration mismatch (a hook{} block
// declared a mode this plugin's Go value never implements), not a
// malformed response or a runtime subscriber failure. For a veto-mode
// request this still fails closed at the kernel exactly like any other
// error response would, per hook-dispatch.md#timeout-behavior.
func errNotImplemented(point commonv1.HookPoint, mode hookv1.HookMode) error {
	return plugin.StatusError(codes.Unimplemented, errorDomain, "mode_not_implemented",
		fmt.Sprintf("%s: %s", ErrSubscriberNotImplemented, mode), errorMetadata(point, mode, hookv1.HookErrorCategory_HOOK_ERROR_CATEGORY_UNKNOWN))
}

// errInvalidRequest builds the error DispatchHook returns when the
// request itself violates the wire contract (hook-dispatch.md's "payload
// MUST be set", "mode MUST be set") — a kernel-side bug, not a subscriber
// failure, so it carries no HookErrorCategory (none of the seven fit a
// malformed *request*). codes.InvalidArgument regardless.
func errInvalidRequest(reason, message string) error {
	return plugin.StatusError(codes.InvalidArgument, errorDomain, reason, message, nil)
}

// mapContextErr translates a context error into the matching gRPC status
// (codes.Canceled / codes.DeadlineExceeded), or returns nil if err is nil
// or not a context error. Used both on ctx.Err() directly and on an
// author-returned error that may simply be the same ctx error bubbled back
// up — .claude/rules/grpc.md: "Cancellation is normal control flow, not an
// error"; never logged or reported as a subscriber failure.
func mapContextErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return nil
	}
}
