//go:build integration

package modeltest_test

import (
	"path/filepath"
	"testing"

	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

// TestRunBinary_reportsALaunchFailureDistinctly asserts that a binary
// which cannot start is reported as a launch error rather than as a
// conformance violation. The two are genuinely different: a binary that
// will not run has not failed the suite, it has failed to be tested.
func TestRunBinary_reportsALaunchFailureDistinctly(t *testing.T) {
	t.Parallel()

	_, err := modeltest.CheckBinary(t.Context(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("CheckBinary() = nil error for a missing binary, want a launch failure")
	}
}
