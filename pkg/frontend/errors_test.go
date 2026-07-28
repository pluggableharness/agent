package frontend_test

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/frontend"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
)

func TestFrontendError_Error(t *testing.T) {
	t.Parallel()

	fe := &frontend.Error{
		Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND,
		Message:  "boom",
	}
	got := fe.Error()
	want := "frontend: FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND: boom"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestFrontendError_StatusErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category frontendv1.FrontendErrorCategory
		wantCode codes.Code
	}{
		{"unspecified", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNSPECIFIED, codes.Internal},
		{"render_failed", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_RENDER_FAILED, codes.Internal},
		{"invalid_request", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_INVALID_REQUEST, codes.InvalidArgument},
		{"unknown", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNKNOWN, codes.Internal},
		{"session_not_found", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND, codes.NotFound},
		{"session_create_failed", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_CREATE_FAILED, codes.InvalidArgument},
		{"session_busy", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_BUSY, codes.FailedPrecondition},
		{"schema_too_new", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SCHEMA_TOO_NEW, codes.FailedPrecondition},
		{"session_replay_only", frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY, codes.FailedPrecondition},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fe := &frontend.Error{Category: tt.category, Message: "detail"}
			err := fe.StatusErr()

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("StatusErr() did not produce a *status.Status-backed error: %v", err)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("StatusErr() code = %v, want %v", st.Code(), tt.wantCode)
			}
			if len(st.Details()) == 0 {
				t.Errorf("StatusErr() carries no structured detail")
			}
		})
	}
}
