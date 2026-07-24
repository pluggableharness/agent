package slashcommand_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/tool"
)

// TestErrorRoundTripThroughToolPackage exercises this package's call sites
// (errors.go, server.go) against tool.NewError/tool.GRPCCode/
// tool.ToStatusError directly, per the "route every error through pkg/tool
// directly, no parallel error type" mandate — the round trip itself is
// exactly the behavior server_test.go's Configure/Invoke tests already
// exercise over the wire; this test isolates the tool.NewError ->
// tool.ToStatusError leg pkg/slashcommand's own code never wraps in
// anything of its own.
func TestErrorRoundTripThroughToolPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category tool.ErrorCategory
		wantCode codes.Code
	}{
		{"invalid_arguments", tool.ErrorCategoryInvalidArguments, codes.InvalidArgument},
		{"not_found", tool.ErrorCategoryNotFound, codes.NotFound},
		{"permission_denied", tool.ErrorCategoryPermissionDenied, codes.PermissionDenied},
		{"execution_failed", tool.ErrorCategoryExecutionFailed, codes.Internal},
		{"timeout", tool.ErrorCategoryTimeout, codes.DeadlineExceeded},
		{"concurrency_conflict", tool.ErrorCategoryConcurrencyConflict, codes.Aborted},
		{"cancelled", tool.ErrorCategoryCancelled, codes.Canceled},
		{"unknown", tool.ErrorCategoryUnknown, codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			te, err := tool.NewError(tt.category, "boom", false, nil)
			if err != nil {
				t.Fatalf("tool.NewError(%v, ...): %v", tt.category, err)
			}
			gotErr := tool.ToStatusError(te)
			st, ok := status.FromError(gotErr)
			if !ok {
				t.Fatalf("tool.ToStatusError(%v) did not produce a *status.Status: %v", te, gotErr)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("code = %v, want %v", st.Code(), tt.wantCode)
			}
			if st.Message() != "boom" {
				t.Errorf("message = %q, want %q", st.Message(), "boom")
			}
		})
	}
}

// TestErrorRoundTripRejectsProcessCrashed asserts this package cannot
// construct a process_crashed *tool.Error either — the kernel-only
// category pkg/tool itself makes unconstructable, unchanged by reuse here.
func TestErrorRoundTripRejectsProcessCrashed(t *testing.T) {
	t.Parallel()

	_, err := tool.NewError(tool.ErrorCategory(8), "subprocess died", false, nil)
	if !errors.Is(err, tool.ErrProcessCrashedCategory) {
		t.Fatalf("tool.NewError(process_crashed numeric value, ...) error = %v, want wrapping %v", err, tool.ErrProcessCrashedCategory)
	}
}
