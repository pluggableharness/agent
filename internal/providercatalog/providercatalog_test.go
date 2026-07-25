package providercatalog_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/pluggableharness/agent/internal/providercatalog"
)

// TestErrNotFoundIsMatchable pins the one behavioral contract this
// package's declarations carry: every driver wraps ErrNotFound with the
// name that missed, and every consumer matches it with errors.Is rather
// than on the message.
func TestErrNotFoundIsMatchable(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("providercatalog/somedriver: tool %q.%q: %w", "fs", "read_file", providercatalog.ErrNotFound)

	if !errors.Is(wrapped, providercatalog.ErrNotFound) {
		t.Fatalf("errors.Is(%v, ErrNotFound) = false, want true", wrapped)
	}
	if errors.Is(errors.New(providercatalog.ErrNotFound.Error()), providercatalog.ErrNotFound) {
		t.Error("a same-message error matched ErrNotFound; the sentinel must be identity-based")
	}
}
