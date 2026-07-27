package modeltest

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestCommandContext_usesAnEnvAllowlistNotTheAmbientEnvironment(t *testing.T) {
	// Not parallel: t.Setenv forbids it.
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("ANTHROPIC_API_KEY", "leaked-credential")

	cmd := commandContext(context.Background(), "/nonexistent/plugin")

	// The allowlist is the point. A plugin that only works because it
	// inherited a credential from the test runner's environment would pass
	// a conformance run and fail under the real kernel, which launches
	// with the same narrow allowlist — the opposite of what conformance is
	// for. Configuration reaches a provider through Configure.
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") {
			t.Errorf("the ambient environment leaked into the subprocess: %q", entry)
		}
	}
	if !slices.Contains(cmd.Env, "PATH=/usr/bin") {
		t.Errorf("PATH was not passed through; got %v", cmd.Env)
	}
	if cmd.Path != "/nonexistent/plugin" && !strings.HasSuffix(cmd.Path, "plugin") {
		t.Errorf("cmd.Path = %q, want the named binary", cmd.Path)
	}
}

func TestAllowedEnv_omitsUnsetKeys(t *testing.T) {
	// Not parallel: t.Setenv forbids it.
	t.Setenv("HOME", "/home/tester")

	for _, entry := range allowedEnv() {
		if strings.HasPrefix(entry, "=") {
			t.Errorf("an unset key produced a malformed entry: %q", entry)
		}
	}
	if !slices.Contains(allowedEnv(), "HOME=/home/tester") {
		t.Error("HOME was not passed through")
	}
}

func TestDiscardLogger_isUsableAndSilent(t *testing.T) {
	t.Parallel()

	// go-plugin requires a non-nil logger; this one exists so its
	// subprocess bookkeeping does not bury a conformance failure in noise.
	logger := discardLogger()
	if logger == nil {
		t.Fatal("discardLogger() = nil, want a usable logger")
	}
	logger.Error("this must not reach the test output")
}
