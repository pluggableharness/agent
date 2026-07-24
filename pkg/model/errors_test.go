package model_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	model "github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func TestModelError_Error(t *testing.T) {
	t.Parallel()

	err := &model.Error{
		Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR,
		Message:  "bad api key",
	}
	if got := err.Error(); got == "" {
		t.Errorf("Error() = %q, want non-empty", got)
	}
}

func TestModelError_StatusError_CodeMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category modelv1.ModelErrorCategory
		wantCode codes.Code
	}{
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED, codes.ResourceExhausted},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, codes.ResourceExhausted},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, codes.Unavailable},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, codes.Unauthenticated},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, codes.InvalidArgument},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED, codes.FailedPrecondition},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN, codes.Internal},
		{modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNSPECIFIED, codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.category.String(), func(t *testing.T) {
			t.Parallel()

			err := &model.Error{Category: tt.category, Message: "boom"}
			st, ok := grpcstatus.FromError(err.StatusError())
			if !ok {
				t.Fatalf("StatusError() did not produce a *status.Status")
			}
			if st.Code() != tt.wantCode {
				t.Errorf("code = %v, want %v", st.Code(), tt.wantCode)
			}
			if len(st.Details()) == 0 {
				t.Errorf("StatusError() carries no structured detail, want ErrorInfo")
			}
		})
	}
}

func TestModelError_StatusError_NeverUnknown(t *testing.T) {
	t.Parallel()

	// An out-of-range category value (never produced by real code, but
	// possible via a stray int32 cast) must still map to Internal, never
	// codes.Unknown, per conformance.md's error taxonomy.
	err := &model.Error{Category: modelv1.ModelErrorCategory(99), Message: "mystery"}
	st, ok := grpcstatus.FromError(err.StatusError())
	if !ok {
		t.Fatalf("StatusError() did not produce a *status.Status")
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want codes.Internal", st.Code())
	}
	if st.Code() == codes.Unknown {
		t.Errorf("code = codes.Unknown, which is never valid per conformance.md")
	}
}

func TestModelError_StatusError_Metadata(t *testing.T) {
	t.Parallel()

	err := &model.Error{
		Category:   modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED,
		Message:    "slow down",
		Retryable:  true,
		RetryAfter: 2 * time.Second,
		RawDetail:  "vendor said 429",
	}
	st, ok := grpcstatus.FromError(err.StatusError())
	if !ok {
		t.Fatalf("StatusError() did not produce a *status.Status")
	}
	if st.Message() != "slow down" {
		t.Errorf("Message() = %q, want %q", st.Message(), "slow down")
	}
}

func TestStatusFromErr_Cancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"raw context.Canceled", context.Canceled},
		{"wrapped context.Canceled", fmt.Errorf("model: stream: %w", context.Canceled)},
		{"already-a-status Canceled", grpcstatus.Error(codes.Canceled, "cancelled upstream")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := model.StatusFromErrForTest(tt.err)
			if grpcstatus.Code(got) != codes.Canceled {
				t.Errorf("code = %v, want codes.Canceled", grpcstatus.Code(got))
			}
		})
	}
}

func TestStatusFromErr_ModelErrorWrapped(t *testing.T) {
	t.Parallel()

	inner := &model.Error{
		Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED,
		Message:  "vendor is down",
	}
	wrapped := fmt.Errorf("adapter: call failed: %w", inner)

	got := model.StatusFromErrForTest(wrapped)
	if grpcstatus.Code(got) != codes.Unavailable {
		t.Errorf("code = %v, want codes.Unavailable", grpcstatus.Code(got))
	}
}

func TestStatusFromErr_UnmappedIsInternal(t *testing.T) {
	t.Parallel()

	got := model.StatusFromErrForTest(errors.New("something unexpected"))
	if grpcstatus.Code(got) != codes.Internal {
		t.Errorf("code = %v, want codes.Internal", grpcstatus.Code(got))
	}
}

func TestStatusFromErr_Nil(t *testing.T) {
	t.Parallel()

	if got := model.StatusFromErrForTest(nil); got != nil {
		t.Errorf("StatusFromErrForTest(nil) = %v, want nil", got)
	}
}
