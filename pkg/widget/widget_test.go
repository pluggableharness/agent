package widget_test

import (
	"testing"

	"github.com/pluggableharness/agent/pkg/widget"
)

func TestUpdateMode_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode widget.UpdateMode
		want string
	}{
		{name: "append is the zero value", mode: widget.UpdateAppend, want: "append"},
		{name: "replace", mode: widget.UpdateReplace, want: "replace"},
		{name: "unrecognized value", mode: widget.UpdateMode(99), want: "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mode.String(); got != tt.want {
				t.Errorf("UpdateMode(%d).String() = %q, want %q", int(tt.mode), got, tt.want)
			}
		})
	}
}

func TestUpdateMode_zeroValueIsAppend(t *testing.T) {
	t.Parallel()

	var m widget.UpdateMode
	if m != widget.UpdateAppend {
		t.Errorf("zero value UpdateMode = %v, want UpdateAppend", m)
	}
}
