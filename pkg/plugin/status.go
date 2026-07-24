package plugin

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StatusError builds a *status.Status-backed error with code and a
// google.rpc.ErrorInfo structured detail (reason, domain, metadata) — the
// canonical "most specific code, never bare codes.Unknown, category enum
// in structured detail" shape .claude/rules/grpc.md mandates for every RPC
// error crossing the plugin boundary. domain should be the calling
// category's own error-taxonomy name, e.g. "tool.pluggableharness.dev".
// metadata MAY be nil when there is no additional structured detail to
// attach.
func StatusError(code codes.Code, domain, reason, message string, metadata map[string]string) error {
	st := status.New(code, message)

	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason:   reason,
		Domain:   domain,
		Metadata: metadata,
	})
	if err != nil {
		// WithDetails only fails if a detail message can't be marshaled
		// into an Any, which cannot happen for a well-formed
		// *errdetails.ErrorInfo built from plain strings — fall back to
		// the detail-less status rather than losing the code and
		// message a caller already has in hand.
		return st.Err()
	}
	return withDetails.Err()
}
