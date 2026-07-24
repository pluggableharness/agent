package slashcommand

import (
	"errors"
	"fmt"

	"github.com/pluggableharness/agent/pkg/tool"
)

// This package declares no SlashCommandError or SlashCommandErrorCategory
// of its own — docs/specifications/slashcommand/conformance.md#error-taxonomy
// is explicit that SlashCommandEvent.error is a
// pluggableharness.tool.v1.ToolError reused verbatim, so its failure
// taxonomy is tool.ErrorCategory, unmodified, and "MUST NOT invent a
// parallel category." Every error server.go's RPC handlers return crosses
// the plugin boundary via tool.NewError/tool.GRPCCode/tool.ToStatusError
// directly; the helpers below only factor out the small, repeated shapes
// server.go's five non-streaming-terminal-event RPC handlers each need,
// rather than inlining the same tool.Error{...} literal five times.

// unknownStatusError wraps err as a tool.ErrorCategoryUnknown *tool.Error
// and converts it to a gRPC status via tool.ToStatusError — the catch-all
// shape a handler uses for an unexpected error a Provider method returned
// with no more specific classification available.
func unknownStatusError(err error) error {
	return tool.ToStatusError(&tool.Error{Category: tool.ErrorCategoryUnknown, Message: err.Error(), Retryable: false})
}

// configureStatusError converts a Provider.Configure error into a gRPC
// status: a *tool.Error is forwarded as-is (its own category, message, and
// retryability preserved), any other error defaults to
// tool.ErrorCategoryInvalidArguments per
// docs/specifications/slashcommand/protocol.md#configure's "MUST reject
// with a structured error on missing required fields rather than
// deferring failure to the first Invoke."
func configureStatusError(err error) error {
	var te *tool.Error
	if errors.As(err, &te) {
		return tool.ToStatusError(te)
	}
	return tool.ToStatusError(&tool.Error{Category: tool.ErrorCategoryInvalidArguments, Message: err.Error(), Retryable: false})
}

// invalidArgumentStatusError builds a gRPC status for a malformed request
// message (e.g. a nil SlashCommandCall) — tool.ErrorCategoryInvalidArguments,
// distinct from configureStatusError in that this is never a
// Provider-returned error, only a wire-decoding failure this package's own
// adapter code detects.
func invalidArgumentStatusError(format string, args ...any) error {
	return tool.ToStatusError(&tool.Error{Category: tool.ErrorCategoryInvalidArguments, Message: fmt.Sprintf(format, args...), Retryable: false})
}
