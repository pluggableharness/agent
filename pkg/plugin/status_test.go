package plugin_test

import (
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pluggableharness/agent/pkg/plugin"
)

func TestStatusError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     codes.Code
		domain   string
		reason   string
		message  string
		metadata map[string]string
	}{
		{
			name:     "resource exhausted with metadata",
			code:     codes.ResourceExhausted,
			domain:   "tool.pluggableharness.dev",
			reason:   "CONTEXT_LENGTH_EXCEEDED",
			message:  "context length exceeded",
			metadata: map[string]string{"limit": "8192"},
		},
		{
			name:    "internal, no metadata",
			code:    codes.Internal,
			domain:  "model.pluggableharness.dev",
			reason:  "UNEXPECTED",
			message: "unexpected failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := plugin.StatusError(tt.code, tt.domain, tt.reason, tt.message, tt.metadata)
			if err == nil {
				t.Fatalf("StatusError(%v, %q, %q, %q, %v) = nil, want non-nil error", tt.code, tt.domain, tt.reason, tt.message, tt.metadata)
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("status.FromError(%v) ok = false, want true", err)
			}
			if got := st.Code(); got != tt.code {
				t.Errorf("Code() = %v, want %v", got, tt.code)
			}
			if got := st.Message(); got != tt.message {
				t.Errorf("Message() = %q, want %q", got, tt.message)
			}

			var found *errdetails.ErrorInfo
			for _, d := range st.Details() {
				info, ok := d.(*errdetails.ErrorInfo)
				if ok {
					found = info
					break
				}
			}
			if found == nil {
				t.Fatalf("Details() contains no *errdetails.ErrorInfo, got %v", st.Details())
			}
			if got, want := found.GetReason(), tt.reason; got != want {
				t.Errorf("ErrorInfo.Reason = %q, want %q", got, want)
			}
			if got, want := found.GetDomain(), tt.domain; got != want {
				t.Errorf("ErrorInfo.Domain = %q, want %q", got, want)
			}
			if got, want := len(found.GetMetadata()), len(tt.metadata); got != want {
				t.Errorf("len(ErrorInfo.Metadata) = %d, want %d", got, want)
			}
			for k, want := range tt.metadata {
				if got := found.GetMetadata()[k]; got != want {
					t.Errorf("ErrorInfo.Metadata[%q] = %q, want %q", k, got, want)
				}
			}
		})
	}
}
