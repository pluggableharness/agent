package context

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This file is a white-box (package context, not context_test) test:
// toStatusError and ErrorCategory's code()/reason() methods are
// unexported translation logic, mirroring convert_internal_test.go's
// rationale.

func TestToStatusError_nil(t *testing.T) {
	t.Parallel()

	if err := toStatusError(nil); err != nil {
		t.Errorf("toStatusError(nil) = %v, want nil", err)
	}
}

func TestToStatusError_categoryMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		category   ErrorCategory
		wantCode   codes.Code
		wantReason string
	}{
		{"source_unavailable", ErrorCategorySourceUnavailable, codes.Unavailable, "source_unavailable"},
		{"budget_exceeded", ErrorCategoryBudgetExceeded, codes.FailedPrecondition, "budget_exceeded"},
		{"scope_violation", ErrorCategoryScopeViolation, codes.PermissionDenied, "scope_violation"},
		{"invalid_request", ErrorCategoryInvalidRequest, codes.InvalidArgument, "invalid_request"},
		{"unknown", ErrorCategoryUnknown, codes.Internal, "unknown"},
		{"unspecified falls back to internal/unknown", ErrorCategoryUnspecified, codes.Internal, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctxErr := &Error{Category: tt.category, Message: "boom", Retryable: true}
			wrapped := toStatusError(ctxErr)
			st, ok := status.FromError(wrapped)
			if !ok {
				t.Fatalf("status.FromError(%v) ok = false, want true", wrapped)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", st.Code(), tt.wantCode)
			}
			if st.Message() != "boom" {
				t.Errorf("Message() = %q, want %q", st.Message(), "boom")
			}

			var info *errdetails.ErrorInfo
			for _, d := range st.Details() {
				if e, ok := d.(*errdetails.ErrorInfo); ok {
					info = e
					break
				}
			}
			if info == nil {
				t.Fatalf("Details() contains no *errdetails.ErrorInfo, got %v", st.Details())
			}
			if info.GetReason() != tt.wantReason {
				t.Errorf("ErrorInfo.Reason = %q, want %q", info.GetReason(), tt.wantReason)
			}
			if info.GetDomain() != errorDomain {
				t.Errorf("ErrorInfo.Domain = %q, want %q", info.GetDomain(), errorDomain)
			}
			if got := info.GetMetadata()["retryable"]; got != "true" {
				t.Errorf("ErrorInfo.Metadata[retryable] = %q, want %q", got, "true")
			}
		})
	}
}

func TestToStatusError_cancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{"canceled", context.Canceled, codes.Canceled},
		{"deadline exceeded", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"wrapped canceled", errors.New("op: " + context.Canceled.Error()), codes.Internal}, // not errors.Is-detectable; falls through to unknown
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, ok := status.FromError(toStatusError(tt.err))
			if !ok {
				t.Fatalf("status.FromError() ok = false, want true")
			}
			if st.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", st.Code(), tt.wantCode)
			}
		})
	}
}

func TestToStatusError_genericError(t *testing.T) {
	t.Parallel()

	st, ok := status.FromError(toStatusError(errors.New("plain failure")))
	if !ok {
		t.Fatalf("status.FromError() ok = false, want true")
	}
	if st.Code() != codes.Internal {
		t.Errorf("Code() = %v, want %v", st.Code(), codes.Internal)
	}
	if st.Message() != "plain failure" {
		t.Errorf("Message() = %q, want %q", st.Message(), "plain failure")
	}
}
