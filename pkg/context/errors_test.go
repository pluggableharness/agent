package context_test

import (
	"testing"

	pluggablecontext "github.com/pluggableharness/agent/pkg/context"
)

func TestContextError_Error(t *testing.T) {
	t.Parallel()

	err := &pluggablecontext.Error{
		Category: pluggablecontext.ErrorCategorySourceUnavailable,
		Message:  "CLAUDE.md deleted mid-session",
	}
	if got, want := err.Error(), "CLAUDE.md deleted mid-session"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
