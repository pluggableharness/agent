package kernel

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// TestBringUp_telemetryOffSelectsTheNoopDriver locks in that
// settings.telemetry = false reaches the discarding backend, without this
// package re-implementing the switch config.TelemetryConfig already owns.
func TestBringUp_telemetryOffSelectsTheNoopDriver(t *testing.T) {
	project := newProject(t, minimalConfig)

	k, err := bringUp(context.Background(), mustNormalize(t, testOptions(t, project, &stringSink{}, &stringSink{})))
	t.Cleanup(func() { _ = k.shutdown(context.Background()) })
	if err != nil {
		t.Fatalf("bringUp: %v", err)
	}
	if got := k.telem.Config().Backend; got != "noop" {
		t.Errorf("telemetry backend = %q, want noop when settings.telemetry = false", got)
	}
	if k.telem.Config().Enabled {
		t.Error("telemetry reported Enabled with settings.telemetry = false")
	}
	if k.bootTelem != nil {
		t.Error("bootstrap telemetry Provider outlived startTelemetry")
	}
	if k.hooks == nil || k.catalog == nil || k.store == nil || k.bus == nil {
		t.Error("bringUp returned without a complete kernel")
	}
}

// TestBringUp_readsGlobalConfigAndLockFile covers the two optional files:
// present here, absent in every other bring-up test.
func TestBringUp_readsGlobalConfigAndLockFile(t *testing.T) {
	project := newProject(t, minimalConfig)
	writeGlobalConfig(t, `
dev_overrides {
  anthropic = "/nonexistent/provider-anthropic"
}
`)
	writeLockFile(t, project, `
lock_file_version = 1

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"
  checksums   = { "linux_amd64" = "sha256:1a2b3c" }
}
`)

	k, err := bringUp(context.Background(), mustNormalize(t, testOptions(t, project, &stringSink{}, &stringSink{})))
	t.Cleanup(func() { _ = k.shutdown(context.Background()) })
	// agent.hcl declares no required_providers, so neither file resolves
	// anything — what this asserts is that both parsed without error.
	if err != nil {
		t.Fatalf("bringUp with a global config and a lock file: %v", err)
	}
}

// TestBringUp_missingProviderIsReportedInFull is the fresh-checkout path:
// a required provider with no lock row must produce one actionable error
// naming it, never a silent hang or a crash on a nil client.
func TestBringUp_missingProviderIsReportedInFull(t *testing.T) {
	project := newProject(t, minimalConfig+`
required_providers {
  anthropic = { source = "github.com/agentco/provider-anthropic", version = "~> 1.0" }
  ripgrep   = { source = "github.com/agentco/provider-ripgrep", version = "~> 2.0" }
}
`)

	k, err := bringUp(context.Background(), mustNormalize(t, testOptions(t, project, &stringSink{}, &stringSink{})))
	t.Cleanup(func() { _ = k.shutdown(context.Background()) })
	if err == nil {
		t.Fatal("bringUp with unresolvable providers succeeded, want an error")
	}

	var missing *providerresolve.MissingError
	if !errors.As(err, &missing) {
		t.Fatalf("bringUp error %T is not a *providerresolve.MissingError", err)
	}
	if len(missing.Missing) != 2 {
		t.Errorf("MissingError names %d providers, want both", len(missing.Missing))
	}
	for _, want := range []string{"anthropic", "ripgrep"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestBringUp_badGlobalConfigIsSurfaced asserts a malformed optional file
// is an error, not silently treated as absent.
func TestBringUp_badGlobalConfigIsSurfaced(t *testing.T) {
	project := newProject(t, minimalConfig)
	writeGlobalConfig(t, "dev_overrides { not valid")

	k, err := bringUp(context.Background(), mustNormalize(t, testOptions(t, project, &stringSink{}, &stringSink{})))
	t.Cleanup(func() { _ = k.shutdown(context.Background()) })
	if err == nil || !strings.Contains(err.Error(), "global config") {
		t.Fatalf("bringUp with a malformed global config = %v, want a global-config error", err)
	}
}

// TestBringUp_badLockFileIsSurfaced asserts the same for the lock file.
func TestBringUp_badLockFileIsSurfaced(t *testing.T) {
	project := newProject(t, minimalConfig)
	writeLockFile(t, project, "lock_file_version = 99")

	k, err := bringUp(context.Background(), mustNormalize(t, testOptions(t, project, &stringSink{}, &stringSink{})))
	t.Cleanup(func() { _ = k.shutdown(context.Background()) })
	if err == nil || !strings.Contains(err.Error(), "lock file") {
		t.Fatalf("bringUp with an unsupported lock file = %v, want a lock-file error", err)
	}
}

// TestOtelLogHandler covers both arms of the logs-signal decision without
// standing up a real OTLP exporter.
func TestOtelLogHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  telemetry.Config
		want bool
	}{
		{"telemetry off", telemetry.Config{Enabled: false, LogsEnabled: true}, false},
		{"logs signal off", enabledConfig(false), false},
		{"both on", enabledConfig(true), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prov, err := telemetry.New(context.Background(), tc.cfg, noop.New(), nil)
			if err != nil {
				t.Fatalf("telemetry.New: %v", err)
			}
			t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })

			k := &kernel{logger: slog.New(slog.DiscardHandler), telem: prov}
			if got := k.otelLogHandler() != nil; got != tc.want {
				t.Errorf("otelLogHandler() != nil = %v, want %v", got, tc.want)
			}
		})
	}
}

// enabledConfig is the minimum valid Config with telemetry on: the
// validator requires a service name and a sampling ratio once Enabled is
// set, neither of which this test has an opinion about.
func enabledConfig(logs bool) telemetry.Config {
	return telemetry.Config{
		Enabled:        true,
		Backend:        "noop",
		SamplingRatio:  1.0,
		LogsEnabled:    logs,
		ExportInterval: time.Second,
		ServiceName:    "kernel-test",
	}
}

// mustNormalize applies Options' own defaulting, so a bringUp test sees
// exactly the Options Run would have handed it.
func mustNormalize(t *testing.T, opts Options) Options {
	t.Helper()
	got, err := opts.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return got
}

// writeGlobalConfig writes body to $XDG_CONFIG_HOME/agent/config.hcl.
func writeGlobalConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "agent")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(body), 0o600); err != nil {
		t.Fatalf("write global config: %v", err)
	}
}

// writeLockFile writes body to <project>/.agent/agent.lock.hcl.
func writeLockFile(t *testing.T, project, body string) {
	t.Helper()
	dir := filepath.Join(project, ".agent")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir .agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.lock.hcl"), []byte(body), 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
}
