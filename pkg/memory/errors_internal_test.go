package memory

import (
	"testing"

	"google.golang.org/grpc/codes"
)

func TestErrorCategory_GRPCCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category ErrorCategory
		want     codes.Code
	}{
		{ErrorCategoryUnspecified, codes.Internal},
		{ErrorCategoryNotFound, codes.NotFound},
		{ErrorCategoryInvalidType, codes.InvalidArgument},
		{ErrorCategoryInvalidScope, codes.InvalidArgument},
		{ErrorCategoryRatificationUnsupported, codes.FailedPrecondition},
		{ErrorCategoryBudgetExceeded, codes.ResourceExhausted},
		{ErrorCategorySourceUnavailable, codes.Unavailable},
		{ErrorCategoryUnknown, codes.Internal},
		{ErrorCategory(99), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.category.String(), func(t *testing.T) {
			t.Parallel()
			if got := tt.category.grpcCode(); got != tt.want {
				t.Errorf("grpcCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestError_GRPCStatus(t *testing.T) {
	t.Parallel()

	err := &Error{Category: ErrorCategoryBudgetExceeded, Message: "too much", Retryable: false}
	grpcErr := err.grpcStatus()
	if grpcErr == nil {
		t.Fatal("grpcStatus() = nil, want a status error")
	}
}
