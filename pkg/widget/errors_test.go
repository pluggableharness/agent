package widget_test

import (
	"errors"
	"testing"

	"github.com/pluggableharness/agent/pkg/widget"
)

func TestError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *widget.Error
		want string
	}{
		{name: "render failed", err: widget.RenderFailed("bad node"), want: "widget: WIDGET_ERROR_CATEGORY_RENDER_FAILED: bad node"},
		{name: "unknown", err: widget.Unknown("boom"), want: "widget: WIDGET_ERROR_CATEGORY_UNKNOWN: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFromStatus_notAStatusError(t *testing.T) {
	t.Parallel()

	_, ok := widget.FromStatus(errors.New("plain error"))
	if ok {
		t.Error("FromStatus(plain error) ok = true, want false")
	}
}

func TestFromStatus_nilError(t *testing.T) {
	t.Parallel()

	_, ok := widget.FromStatus(nil)
	if ok {
		t.Error("FromStatus(nil) ok = true, want false")
	}
}
