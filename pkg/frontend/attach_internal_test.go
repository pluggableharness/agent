package frontend

import (
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTerminal(t *testing.T) {
	t.Parallel()

	if got := terminal(io.EOF); got != nil {
		t.Errorf("terminal(io.EOF) = %v, want nil", got)
	}

	canceled := status.Error(codes.Canceled, "client canceled")
	if got := terminal(canceled); got != nil {
		t.Errorf("terminal(Canceled) = %v, want nil", got)
	}

	other := errors.New("transport broke")
	if got := terminal(other); got != other { //nolint:errorlint // exact identity, not classification, is what's under test
		t.Errorf("terminal(other) = %v, want %v", got, other)
	}
}

func TestRequestIDOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event ClientEvent
		want  *string
	}{
		{"create_session", ClientEvent{Payload: CreateSession{RequestID: "r1"}}, strPtr("r1")},
		{"attach_session", ClientEvent{Payload: AttachSession{RequestID: "r2"}}, strPtr("r2")},
		{"resume_session", ClientEvent{Payload: ResumeSession{RequestID: "r3"}}, strPtr("r3")},
		{"detach_session", ClientEvent{Payload: DetachSession{RequestID: "r4"}}, strPtr("r4")},
		{"list_sessions", ClientEvent{Payload: ListSessions{RequestID: "r5"}}, strPtr("r5")},
		{"user_message has no request_id", ClientEvent{Payload: UserMessage{}}, nil},
		{"hello has no request_id", ClientEvent{Payload: Hello{}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := requestIDOf(tt.event)
			switch {
			case got == nil && tt.want == nil:
			case got == nil || tt.want == nil:
				t.Errorf("requestIDOf() = %v, want %v", got, tt.want)
			case *got != *tt.want:
				t.Errorf("requestIDOf() = %q, want %q", *got, *tt.want)
			}
		})
	}
}
