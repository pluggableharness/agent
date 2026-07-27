//go:build integration

package modeltest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

// TestRunBinary_againstTheExampleProvider drives the conformance suite
// through a real handshake and subprocess, against a binary built from
// examples/provider.
//
// This is the only path that exercises a plugin's own main() wiring — its
// handshake config, its Serve call, its identity stamping — none of which
// the in-process mode touches. It is also the mode a plugin written in
// another language would be checked by, since it speaks nothing but the
// wire protocol.
//
// The example is used as the subject because it is already this
// repository's proof that pkg/ works from outside the main module;
// running the suite against it closes the loop.
func TestRunBinary_againstTheExampleProvider(t *testing.T) {
	binary := buildExampleProvider(t)
	modeltest.RunBinary(t, binary)
}

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

// buildExampleProvider compiles examples/provider into the repo's bin/
// and returns the path.
//
// bin/, not t.TempDir(): the project CLAUDE.md's "bin/ only, no
// exceptions" rule covers test fixtures too, even where a temp dir would
// be the obvious choice.
func buildExampleProvider(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	out := filepath.Join(root, "bin", "modeltest-example-provider")

	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", out, ".")
	cmd.Dir = filepath.Join(root, "examples", "provider")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the example provider: %v\n%s", err, combined)
	}
	t.Cleanup(func() { _ = os.Remove(out) })
	return out
}
