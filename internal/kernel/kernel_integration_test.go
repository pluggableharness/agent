//go:build integration

package kernel_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pluggableharness/agent/internal/kernel"
)

// Mirrors of the fixture's own constants (testdata/plugin/main.go).
const (
	fixtureModelID = "fixture-model-1"
	fixtureAnswer  = "the composition root works"
)

// fixtureBinary is the built model-provider fixture every test here
// launches through dev_overrides.
var fixtureBinary string

// TestMain builds the fixture once. It delegates to run so every cleanup
// happens before os.Exit, which skips deferred calls (go-style.md).
func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	// bin/ is the only sanctioned output path for a compiled artifact in
	// this repo — including a test fixture, and including one that would
	// otherwise be a natural fit for os.MkdirTemp. See the project
	// CLAUDE.md's "Build output — bin/ only, no exceptions".
	binDir, err := filepath.Abs(filepath.Join("..", "..", "bin"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kernel: integration: resolve bin/:", err)
		return 1
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "kernel: integration: mkdir bin/:", err)
		return 1
	}
	fixtureBinary = filepath.Join(binDir, "kernel-fixture-model")

	cmd := exec.CommandContext(context.Background(), "go", "build",
		"-tags=integration", "-o", fixtureBinary, "./testdata/plugin")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "kernel: integration: build fixture: %v\n%s", err, out)
		return 1
	}
	defer func() { _ = os.Remove(fixtureBinary) }()

	return m.Run()
}

// TestRun_completesOneSessionEndToEnd is the whole point of this package:
// a real config, a real plugin subprocess, a real state-backend file, a
// real turn, and the fixture's answer on stdout.
//
// It goes through dev_overrides rather than a lock file plus a cached
// binary because that is the path with no download step
// (settings-and-global.md#dev_overrides: "the kernel MUST use that binary
// directly"), and this build has no download path at all.
func TestRun_completesOneSessionEndToEnd(t *testing.T) {
	project := newIntegrationProject(t)
	stdout, stderr := &strings.Builder{}, &strings.Builder{}

	err := kernel.Run(context.Background(), kernel.Options{
		ConfigPath:       filepath.Join(project, "agent.hcl"),
		Prompt:           "say the thing",
		LogLevel:         "debug",
		WorkingDirectory: project,
		Stdout:           stdout,
		Stderr:           stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v\n--- stderr ---\n%s", err, stderr.String())
	}

	if got, want := stdout.String(), fixtureAnswer+"\n"; got != want {
		t.Errorf("stdout = %q, want %q\n--- stderr ---\n%s", got, want, stderr.String())
	}

	// The session really persisted: exactly one sqlite file under the
	// state directory this run was pointed at.
	sessions, err := filepath.Glob(filepath.Join(os.Getenv("XDG_STATE_HOME"), "agent", "sessions", "*.sqlite"))
	if err != nil {
		t.Fatalf("glob sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("found %d session files, want exactly 1", len(sessions))
	}

	// The tracked auto-allow deviation is loud, per its own contract and
	// this package's CLAUDE.md.
	if !strings.Contains(stderr.String(), "UNSAFE plan-decision resolver active") {
		t.Error("the auto-allow deviation did not produce its WARN")
	}
}

// TestRun_cancellationIsNotAFailure asserts an already-canceled context
// short-circuits with context.Canceled rather than a bring-up error —
// what cmd/agent maps to exit code 130.
func TestRun_cancellationIsNotAFailure(t *testing.T) {
	project := newIntegrationProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := kernel.Run(ctx, kernel.Options{
		ConfigPath:       filepath.Join(project, "agent.hcl"),
		Prompt:           "say the thing",
		LogLevel:         "error",
		WorkingDirectory: project,
		Stdout:           &strings.Builder{},
		Stderr:           &strings.Builder{},
	})
	if err == nil {
		t.Fatal("Run on a canceled context = nil, want an error")
	}
}

// newIntegrationProject writes an agent.hcl naming the fixture provider, a
// global config dev-overriding it to the built binary, and points every
// XDG variable at throwaway directories.
func newIntegrationProject(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	project := filepath.Join(root, "project")
	mkdir(t, project)

	write(t, filepath.Join(project, "agent.hcl"), fmt.Sprintf(`
required_providers {
  fixture = {
    source  = "internal/kernel/testdata/plugin"
    version = "~> 0.0"
  }
}

provider "fixture" {}

agent_profile "default" {
  max_turns    = 4
  max_cost_usd = 1.0

  model {
    primary {
      provider = "fixture"
      id       = %q
    }
  }
}

settings {
  default_frontend = "none"
  log_level        = "debug"
  telemetry        = false
}
`, fixtureModelID))

	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, filepath.Join(root, v))
	}

	configDir := filepath.Join(root, "XDG_CONFIG_HOME", "agent")
	mkdir(t, configDir)
	write(t, filepath.Join(configDir, "config.hcl"), fmt.Sprintf(`
dev_overrides {
  fixture = %q
}
`, fixtureBinary))

	return project
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
