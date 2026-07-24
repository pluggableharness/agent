package tool_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/tool"
)

func TestErrorCategoryString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category tool.ErrorCategory
		want     string
	}{
		{"unspecified", tool.ErrorCategoryUnspecified, "unspecified"},
		{"invalid_arguments", tool.ErrorCategoryInvalidArguments, "invalid_arguments"},
		{"not_found", tool.ErrorCategoryNotFound, "not_found"},
		{"permission_denied", tool.ErrorCategoryPermissionDenied, "permission_denied"},
		{"execution_failed", tool.ErrorCategoryExecutionFailed, "execution_failed"},
		{"timeout", tool.ErrorCategoryTimeout, "timeout"},
		{"concurrency_conflict", tool.ErrorCategoryConcurrencyConflict, "concurrency_conflict"},
		{"cancelled", tool.ErrorCategoryCancelled, "cancelled"},
		{"unknown", tool.ErrorCategoryUnknown, "unknown"},
		{"out of range", tool.ErrorCategory(99), "unrecognized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.category.String(); got != tt.want {
				t.Errorf("ErrorCategory(%d).String() = %q, want %q", tt.category, got, tt.want)
			}
		})
	}
}

func TestNewError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		category  tool.ErrorCategory
		message   string
		retryable bool
		details   map[string]any
		wantErr   error
	}{
		{
			name:      "valid invalid_arguments",
			category:  tool.ErrorCategoryInvalidArguments,
			message:   "bad path",
			retryable: false,
		},
		{
			name:      "valid timeout retryable",
			category:  tool.ErrorCategoryTimeout,
			message:   "deadline exceeded",
			retryable: true,
			details:   map[string]any{"elapsed_ms": 5000},
		},
		{
			name:     "empty message rejected",
			category: tool.ErrorCategoryNotFound,
			message:  "",
			wantErr:  tool.ErrEmptyMessage,
		},
		{
			name:     "unspecified category rejected",
			category: tool.ErrorCategoryUnspecified,
			message:  "whatever",
			wantErr:  tool.ErrUnspecifiedCategory,
		},
		{
			// process_crashed's underlying int value (8) is not
			// exported, but ErrorCategory is just an int — a
			// caller can still name the numeric value directly.
			// NewError MUST refuse it regardless.
			name:     "process_crashed numeric value rejected",
			category: tool.ErrorCategory(8),
			message:  "subprocess died",
			wantErr:  tool.ErrProcessCrashedCategory,
		},
		{
			name:     "out of range category rejected",
			category: tool.ErrorCategory(99),
			message:  "whatever",
			wantErr:  tool.ErrUnspecifiedCategory,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tool.NewError(tt.category, tt.message, tt.retryable, tt.details)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewError(%v, %q, ...) error = %v, want wrapping %v", tt.category, tt.message, err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("NewError(%v, %q, ...) = %v, want nil on error", tt.category, tt.message, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewError(%v, %q, ...) unexpected error: %v", tt.category, tt.message, err)
			}
			if got.Category != tt.category {
				t.Errorf("Category = %v, want %v", got.Category, tt.category)
			}
			if got.Message != tt.message {
				t.Errorf("Message = %q, want %q", got.Message, tt.message)
			}
			if got.Retryable != tt.retryable {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tt.retryable)
			}
		})
	}
}

func TestErrorImplementsError(t *testing.T) {
	t.Parallel()

	te, err := tool.NewError(tool.ErrorCategoryExecutionFailed, "compile failed", false, nil)
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	var asErr error = te
	if asErr.Error() != "compile failed" {
		t.Errorf("te.Error() = %q, want %q", asErr.Error(), "compile failed")
	}

	var nilTE *tool.Error
	if nilTE.Error() != "" {
		t.Errorf("nil Error.Error() = %q, want empty string", nilTE.Error())
	}
}

func TestGRPCCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category tool.ErrorCategory
		want     codes.Code
	}{
		{tool.ErrorCategoryInvalidArguments, codes.InvalidArgument},
		{tool.ErrorCategoryNotFound, codes.NotFound},
		{tool.ErrorCategoryPermissionDenied, codes.PermissionDenied},
		{tool.ErrorCategoryExecutionFailed, codes.Internal},
		{tool.ErrorCategoryTimeout, codes.DeadlineExceeded},
		{tool.ErrorCategoryConcurrencyConflict, codes.Aborted},
		{tool.ErrorCategoryCancelled, codes.Canceled},
		{tool.ErrorCategoryUnknown, codes.Internal},
		{tool.ErrorCategoryUnspecified, codes.Internal},
		{tool.ErrorCategory(99), codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.category.String(), func(t *testing.T) {
			t.Parallel()
			if got := tool.GRPCCode(tt.category); got != tt.want {
				t.Errorf("GRPCCode(%v) = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}

func TestToStatusError(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		err := tool.ToStatusError(nil)
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("ToStatusError(nil) did not produce a *status.Status: %v", err)
		}
		if st.Code() != codes.Internal {
			t.Errorf("ToStatusError(nil) code = %v, want %v", st.Code(), codes.Internal)
		}
	})

	t.Run("with details", func(t *testing.T) {
		t.Parallel()
		te, err := tool.NewError(tool.ErrorCategoryNotFound, "no such file", false, map[string]any{"path": "/tmp/x"})
		if err != nil {
			t.Fatalf("NewError: %v", err)
		}
		gotErr := tool.ToStatusError(te)
		st, ok := status.FromError(gotErr)
		if !ok {
			t.Fatalf("ToStatusError did not produce a *status.Status: %v", gotErr)
		}
		if st.Code() != codes.NotFound {
			t.Errorf("code = %v, want %v", st.Code(), codes.NotFound)
		}
		if st.Message() != "no such file" {
			t.Errorf("message = %q, want %q", st.Message(), "no such file")
		}
	})
}
