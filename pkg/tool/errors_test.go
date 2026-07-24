package tool_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/tool"
)

func TestToolErrorCategoryString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		category tool.ToolErrorCategory
		want     string
	}{
		{"unspecified", tool.ToolErrorCategoryUnspecified, "unspecified"},
		{"invalid_arguments", tool.ToolErrorCategoryInvalidArguments, "invalid_arguments"},
		{"not_found", tool.ToolErrorCategoryNotFound, "not_found"},
		{"permission_denied", tool.ToolErrorCategoryPermissionDenied, "permission_denied"},
		{"execution_failed", tool.ToolErrorCategoryExecutionFailed, "execution_failed"},
		{"timeout", tool.ToolErrorCategoryTimeout, "timeout"},
		{"concurrency_conflict", tool.ToolErrorCategoryConcurrencyConflict, "concurrency_conflict"},
		{"cancelled", tool.ToolErrorCategoryCancelled, "cancelled"},
		{"unknown", tool.ToolErrorCategoryUnknown, "unknown"},
		{"out of range", tool.ToolErrorCategory(99), "unrecognized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.category.String(); got != tt.want {
				t.Errorf("ToolErrorCategory(%d).String() = %q, want %q", tt.category, got, tt.want)
			}
		})
	}
}

func TestNewToolError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		category  tool.ToolErrorCategory
		message   string
		retryable bool
		details   map[string]any
		wantErr   error
	}{
		{
			name:      "valid invalid_arguments",
			category:  tool.ToolErrorCategoryInvalidArguments,
			message:   "bad path",
			retryable: false,
		},
		{
			name:      "valid timeout retryable",
			category:  tool.ToolErrorCategoryTimeout,
			message:   "deadline exceeded",
			retryable: true,
			details:   map[string]any{"elapsed_ms": 5000},
		},
		{
			name:     "empty message rejected",
			category: tool.ToolErrorCategoryNotFound,
			message:  "",
			wantErr:  tool.ErrEmptyMessage,
		},
		{
			name:     "unspecified category rejected",
			category: tool.ToolErrorCategoryUnspecified,
			message:  "whatever",
			wantErr:  tool.ErrUnspecifiedCategory,
		},
		{
			// process_crashed's underlying int value (8) is not
			// exported, but ToolErrorCategory is just an int — a
			// caller can still name the numeric value directly.
			// NewToolError MUST refuse it regardless.
			name:     "process_crashed numeric value rejected",
			category: tool.ToolErrorCategory(8),
			message:  "subprocess died",
			wantErr:  tool.ErrProcessCrashedCategory,
		},
		{
			name:     "out of range category rejected",
			category: tool.ToolErrorCategory(99),
			message:  "whatever",
			wantErr:  tool.ErrUnspecifiedCategory,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tool.NewToolError(tt.category, tt.message, tt.retryable, tt.details)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewToolError(%v, %q, ...) error = %v, want wrapping %v", tt.category, tt.message, err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("NewToolError(%v, %q, ...) = %v, want nil on error", tt.category, tt.message, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewToolError(%v, %q, ...) unexpected error: %v", tt.category, tt.message, err)
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

func TestToolErrorImplementsError(t *testing.T) {
	t.Parallel()

	te, err := tool.NewToolError(tool.ToolErrorCategoryExecutionFailed, "compile failed", false, nil)
	if err != nil {
		t.Fatalf("NewToolError: %v", err)
	}
	var asErr error = te
	if asErr.Error() != "compile failed" {
		t.Errorf("te.Error() = %q, want %q", asErr.Error(), "compile failed")
	}

	var nilTE *tool.ToolError
	if nilTE.Error() != "" {
		t.Errorf("nil ToolError.Error() = %q, want empty string", nilTE.Error())
	}
}

func TestGRPCCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category tool.ToolErrorCategory
		want     codes.Code
	}{
		{tool.ToolErrorCategoryInvalidArguments, codes.InvalidArgument},
		{tool.ToolErrorCategoryNotFound, codes.NotFound},
		{tool.ToolErrorCategoryPermissionDenied, codes.PermissionDenied},
		{tool.ToolErrorCategoryExecutionFailed, codes.Internal},
		{tool.ToolErrorCategoryTimeout, codes.DeadlineExceeded},
		{tool.ToolErrorCategoryConcurrencyConflict, codes.Aborted},
		{tool.ToolErrorCategoryCancelled, codes.Canceled},
		{tool.ToolErrorCategoryUnknown, codes.Internal},
		{tool.ToolErrorCategoryUnspecified, codes.Internal},
		{tool.ToolErrorCategory(99), codes.Internal},
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
		te, err := tool.NewToolError(tool.ToolErrorCategoryNotFound, "no such file", false, map[string]any{"path": "/tmp/x"})
		if err != nil {
			t.Fatalf("NewToolError: %v", err)
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
