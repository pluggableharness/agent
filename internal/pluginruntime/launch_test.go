package pluginruntime

import (
	"errors"
	"testing"

	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

func TestConfig_validate(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	valid := Config{
		BinaryPath: "/bin/true",
		Producer:   &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_TOOL, Name: "x", Version: "1.0.0"},
		Callback:   &fakeCallbackServer{},
		Telemetry:  prov,
	}

	tests := []struct {
		name    string
		mutate  func(c Config) Config
		wantErr error
	}{
		{"valid", func(c Config) Config { return c }, nil},
		{"missing binary path", func(c Config) Config { c.BinaryPath = ""; return c }, ErrMissingBinaryPath},
		{"missing producer", func(c Config) Config { c.Producer = nil; return c }, ErrMissingProducer},
		{"missing callback", func(c Config) Config { c.Callback = nil; return c }, ErrMissingCallback},
		{"missing telemetry", func(c Config) Config { c.Telemetry = nil; return c }, ErrMissingTelemetry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.mutate(valid).validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}

func TestPreflightVersionCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		declared uint32
		wantErr  bool
	}{
		{"zero is a no-op today", 0, false},
		{"matching version passes", uint32(common.ProtocolVersion), false},
		{"mismatched version rejects", uint32(common.ProtocolVersion) + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			producer := &commonv1.ProducerRef{ProtocolVersion: tt.declared}
			err := preflightVersionCheck(producer)
			if tt.wantErr {
				var vme *VersionMismatchError
				if !errors.As(err, &vme) {
					t.Fatalf("preflightVersionCheck() = %v, want *VersionMismatchError", err)
				}
				if vme.Declared != int(tt.declared) {
					t.Errorf("Declared = %d, want %d", vme.Declared, tt.declared)
				}
				return
			}
			if err != nil {
				t.Fatalf("preflightVersionCheck() = %v, want nil", err)
			}
		})
	}
}

func TestVersionMismatchError_message(t *testing.T) {
	t.Parallel()

	err := &VersionMismatchError{Declared: 2, Kernel: 1}
	if err.Error() == "" {
		t.Error("Error() is empty")
	}
}

func TestBuildEnv(t *testing.T) {
	// No t.Parallel(): t.Setenv forbids it (internal/telemetry/CLAUDE.md
	// hit this same pitfall — an env-mutating test races unrelated
	// parallel tests reading the ambient environment).
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/test")
	t.Setenv("TMPDIR", "/tmp")

	env := buildEnv(commonv1.Category_CATEGORY_TOOL, "ripgrep", "1.2.3", []string{"EXTRA=1"})

	want := map[string]string{
		"PATH":   "/usr/bin",
		"HOME":   "/home/test",
		"TMPDIR": "/tmp",
		"EXTRA":  "1",
	}
	got := envToMap(t, env)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["OTEL_RESOURCE_ATTRIBUTES"]; !ok {
		t.Error("OTEL_RESOURCE_ATTRIBUTES missing from env")
	}
}

func TestBuildEnv_neverInheritsFullEnviron(t *testing.T) {
	// No t.Parallel(): see TestBuildEnv.
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/test")
	t.Setenv("TMPDIR", "/tmp")
	t.Setenv("PLUGGABLEHARNESS_AGENT_PLUGINRUNTIME_TEST_SECRET", "must-not-leak")

	env := buildEnv(commonv1.Category_CATEGORY_TOOL, "ripgrep", "1.2.3", nil)
	got := envToMap(t, env)

	if _, ok := got["PLUGGABLEHARNESS_AGENT_PLUGINRUNTIME_TEST_SECRET"]; ok {
		t.Error("buildEnv leaked an arbitrary kernel env var — must only allowlist PATH/HOME/TMPDIR plus the OTel stamp and ExtraEnv")
	}
	if len(got) != 4 {
		t.Errorf("buildEnv produced %d entries (%v), want exactly 4 (PATH, HOME, TMPDIR, OTEL_RESOURCE_ATTRIBUTES) — no extra ambient env leaked through", len(got), got)
	}
}

func TestLaunch_rejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	_, err := Launch(t.Context(), Config{})
	if !errors.Is(err, ErrMissingBinaryPath) {
		t.Fatalf("Launch() = %v, want errors.Is ErrMissingBinaryPath", err)
	}
}

func TestLaunch_preflightRejectsMismatchBeforeSpawning(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	cfg := Config{
		BinaryPath: "/nonexistent/should-never-be-exec-d",
		Producer: &commonv1.ProducerRef{
			Category:        commonv1.Category_CATEGORY_TOOL,
			Name:            "x",
			Version:         "1.0.0",
			ProtocolVersion: uint32(common.ProtocolVersion) + 1,
		},
		Callback:  &fakeCallbackServer{},
		Telemetry: prov,
	}

	_, err := Launch(t.Context(), cfg)
	var vme *VersionMismatchError
	if !errors.As(err, &vme) {
		t.Fatalf("Launch() = %v, want *VersionMismatchError (rejected before any spawn attempt)", err)
	}
}

func TestBuildClient_doesNotStartAnything(t *testing.T) {
	t.Parallel()

	prov := newTestTelemetry(t)
	cfg := Config{
		BinaryPath: "/nonexistent/never-exec-d",
		Producer: &commonv1.ProducerRef{
			Category: commonv1.Category_CATEGORY_TOOL,
			Name:     "x",
			Version:  "1.0.0",
		},
		Callback:  &fakeCallbackServer{},
		Telemetry: prov,
	}

	client, cancel := buildClient(t.Context(), cfg, nil)
	defer cancel()

	if client == nil {
		t.Fatal("buildClient returned a nil client")
	}
	// plugin.NewClient only builds a struct — nothing has been started,
	// so Exited() must report false and NegotiatedVersion() its
	// not-yet-negotiated zero value, confirming buildClient never
	// spawned or dialed anything.
	if client.Exited() {
		t.Error("client.Exited() = true, want false: buildClient must not start the subprocess")
	}
	if v := client.NegotiatedVersion(); v != 0 {
		t.Errorf("client.NegotiatedVersion() = %d, want 0: buildClient must not have dialed", v)
	}
}

func TestPlugin_accessors(t *testing.T) {
	t.Parallel()

	producer := &commonv1.ProducerRef{Name: "x", Version: "1.0.0"}
	dispensed := "raw-client-stand-in"
	p := &Plugin{dispensed: dispensed, producer: producer}

	if got := p.Dispensed(); got != dispensed {
		t.Errorf("Dispensed() = %v, want %v", got, dispensed)
	}
	if got := p.Producer(); got != producer {
		t.Errorf("Producer() = %v, want %v", got, producer)
	}
}

// envToMap parses a "KEY=VALUE" slice, as built by buildEnv, into a map
// for easy lookups in assertions.
func envToMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		for i := range kv {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
