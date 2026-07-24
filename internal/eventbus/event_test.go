package eventbus

import (
	"errors"
	"testing"
)

func TestEvent_validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   Event
		wantErr error
	}{
		{name: "valid with payload", event: Event{Topic: "tool.result", Payload: 42}},
		{name: "valid with nil payload", event: Event{Topic: "tool.result"}},
		{name: "empty topic", event: Event{Topic: "", Payload: 42}, wantErr: ErrEmptyTopic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.event.validate()
			if tt.wantErr == nil && err != nil {
				t.Errorf("validate() = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("validate() = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}
