package memory_test

import (
	"context"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/memory"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

func TestErrorCategory_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		category memory.ErrorCategory
		want     string
	}{
		{memory.ErrorCategoryUnspecified, "unspecified"},
		{memory.ErrorCategoryNotFound, "not_found"},
		{memory.ErrorCategoryInvalidType, "invalid_type"},
		{memory.ErrorCategoryInvalidScope, "invalid_scope"},
		{memory.ErrorCategoryRatificationUnsupported, "ratification_unsupported"},
		{memory.ErrorCategoryBudgetExceeded, "budget_exceeded"},
		{memory.ErrorCategorySourceUnavailable, "source_unavailable"},
		{memory.ErrorCategoryUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.category.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestError_Error(t *testing.T) {
	t.Parallel()

	err := memory.NotFound("no such record")
	want := "memory: not_found: no such record"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestErrorMapping_ThroughRPCBoundary drives every constructor through a
// real RPC round trip (via GetRecord, which the empty-id short-circuit in
// server.go doesn't intercept once an id is supplied) and asserts the
// resulting gRPC code matches conformance.md's table exactly — the mapping
// this package's whole raison d'être depends on getting right.
func TestErrorMapping_ThroughRPCBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		build     func() *memory.Error
		wantCode  codes.Code
		wantRetry bool
	}{
		{"not_found", func() *memory.Error { return memory.NotFound("x") }, codes.NotFound, false},
		{"invalid_type", func() *memory.Error { return memory.InvalidType("x") }, codes.InvalidArgument, false},
		{"invalid_scope", func() *memory.Error { return memory.InvalidScope("x") }, codes.InvalidArgument, false},
		{"ratification_unsupported", func() *memory.Error { return memory.RatificationUnsupported("x") }, codes.FailedPrecondition, false},
		{"budget_exceeded", func() *memory.Error { return memory.BudgetExceeded("x") }, codes.ResourceExhausted, false},
		{"source_unavailable", func() *memory.Error { return memory.SourceUnavailable("x") }, codes.Unavailable, true},
		{"unknown", func() *memory.Error { return memory.Unknown("x") }, codes.Internal, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			domainErr := tt.build()
			provider := &fakeProvider{getRecordFunc: func(context.Context, string) (memory.Record, error) {
				return memory.Record{}, domainErr
			}}
			client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
			_, err := client.GetRecord(t.Context(), &memoryv1.GetRecordRequest{Id: "r1"})

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("error = %v, not a gRPC status error", err)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("code = %v, want %v", st.Code(), tt.wantCode)
			}

			var info *errdetails.ErrorInfo
			for _, d := range st.Details() {
				if ei, ok := d.(*errdetails.ErrorInfo); ok {
					info = ei
				}
			}
			if info == nil {
				t.Fatalf("status has no ErrorInfo detail")
			}
			if info.GetDomain() != "memory.pluggableharness.dev" {
				t.Errorf("ErrorInfo.Domain = %q, want %q", info.GetDomain(), "memory.pluggableharness.dev")
			}
			if info.GetReason() != domainErr.Category.String() {
				t.Errorf("ErrorInfo.Reason = %q, want %q", info.GetReason(), domainErr.Category.String())
			}
			wantRetryable := "false"
			if tt.wantRetry {
				wantRetryable = "true"
			}
			if got := info.GetMetadata()["retryable"]; got != wantRetryable {
				t.Errorf("ErrorInfo.Metadata[retryable] = %q, want %q", got, wantRetryable)
			}
		})
	}
}

func TestErrorMapping_PlainErrorIsUnknownInternal(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{getRecordFunc: func(context.Context, string) (memory.Record, error) {
		return memory.Record{}, errInjected
	}}
	client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
	_, err := client.GetRecord(t.Context(), &memoryv1.GetRecordRequest{Id: "r1"})
	assertCode(t, err, codes.Internal)
}
