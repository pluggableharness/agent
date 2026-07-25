package pluginhost

// Unit tier: the bring-up sequence itself, driven through the Supervisor
// launch seam against an in-process gRPC client (rpc_test.go's dial) and
// a recording teardown. Everything the sequence does after the
// subprocess exists — Describe, reconcile, checksum verify, schema
// fetch, config decode, the slot install, Configure, register, and every
// failure path's teardown — is real here; only the fork/exec is faked.
// The real spawn is integration-tier.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/registry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// fakeLaunch records every teardown and hands back an in-process tool
// client, so the whole post-spawn sequence runs for real.
type fakeLaunch struct {
	mu       sync.Mutex
	torndown []string
	launches int

	client any
	err    error
}

func (f *fakeLaunch) fn(_ context.Context, resolved providerresolve.Resolved, _ *callbackSlot) (*launchedPlugin, error) {
	f.mu.Lock()
	f.launches++
	f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}
	return &launchedPlugin{
		client: f.client,
		close: func(context.Context) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.torndown = append(f.torndown, resolved.LocalName)
			return nil
		},
	}, nil
}

func (f *fakeLaunch) teardowns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.torndown))
	copy(out, f.torndown)
	return out
}

// toolFixture writes a real file so checksum verification has something
// to hash, and returns a Resolved whose lock row records its true digest.
func toolFixture(t *testing.T, localName string) providerresolve.Resolved {
	t.Helper()

	path := filepath.Join(t.TempDir(), "binary")
	content := []byte("not really a binary, but a real file to hash\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	sum := sha256.Sum256(content)
	locked := registry.LockedProvider{
		Source:    "github.com/agentco/fake",
		Version:   "1.0.0",
		Checksums: map[string]string{"linux_amd64": "sha256:" + hex.EncodeToString(sum[:])},
	}
	return providerresolve.Resolved{
		LocalName:  localName,
		Source:     locked.Source,
		Version:    locked.Version,
		Category:   commonv1.Category_CATEGORY_TOOL,
		BinaryPath: path,
		Platform:   "linux_amd64",
		Locked:     &locked,
	}
}

// startHarness wires a Supervisor over resolved with a fake launch
// returning a client backed by an in-process ToolService.
func startHarness(t *testing.T, resolved []providerresolve.Resolved) (*Supervisor, *fakeLaunch) {
	t.Helper()

	recorded := &configured{}
	conn := dial(t, func(s *grpc.Server) {
		toolv1.RegisterToolServiceServer(s, &fakeTool{cfg: recorded})
	})

	cfg := testDeps(t)
	cfg.Resolved = resolved
	s, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}

	launcher := &fakeLaunch{client: toolv1.NewToolServiceClient(conn)}
	s.launch = launcher.fn
	return s, launcher
}

func TestStartOne_fullSequence(t *testing.T) {
	resolved := toolFixture(t, "fixture")
	// fakeTool describes itself as fake-CATEGORY_TOOL from
	// github.com/agentco/fake at 1.0.0, matching the lock row above.
	s, launcher := startHarness(t, []providerresolve.Resolved{resolved})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	live, ok := s.cfg.Registry.ByLocalName("fixture")
	if !ok {
		t.Fatal("ByLocalName(fixture) reported ok = false after a successful Start")
	}
	if live.Producer.GetName() != "fake-"+commonv1.Category_CATEGORY_TOOL.String() {
		t.Errorf("Producer.Name = %q, want the described identity", live.Producer.GetName())
	}
	if live.LaunchIndex != 0 {
		t.Errorf("LaunchIndex = %d, want 0", live.LaunchIndex)
	}
	if live.ConfigSchema == nil {
		t.Error("ConfigSchema is nil, want the fetched schema")
	}
	if _, ok := live.Capabilities.(*toolv1.GetSchemaResponse); !ok {
		t.Errorf("Capabilities = %T, want the whole *toolv1.GetSchemaResponse", live.Capabilities)
	}
	if got := launcher.teardowns(); len(got) != 0 {
		t.Errorf("teardowns = %v after a successful Start, want none", got)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := launcher.teardowns(); len(got) != 1 || got[0] != "fixture" {
		t.Errorf("teardowns = %v, want [fixture]", got)
	}
}

// TestStartOne_devOverrideSkipsChecksum confirms a dev-override provider
// is brought up with no lock row and no checksum verification — the
// bypass settings-and-global.md#dev_overrides specifies.
func TestStartOne_devOverrideSkipsChecksum(t *testing.T) {
	resolved := providerresolve.Resolved{
		LocalName:      "dev",
		Source:         "github.com/agentco/fake",
		Category:       commonv1.Category_CATEGORY_TOOL,
		BinaryPath:     "/nonexistent/path/that/would/fail/a/checksum",
		ViaDevOverride: true,
	}
	s, _ := startHarness(t, []providerresolve.Resolved{resolved})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start with a dev override: %v — a dev override must skip checksum verification entirely", err)
	}
	if _, ok := s.cfg.Registry.ByLocalName("dev"); !ok {
		t.Error("the dev-override provider was not registered")
	}
}

func TestStart_failurePathsTearEverythingDown(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*providerresolve.Resolved)
		wantErr error
	}{
		{
			name: "identity mismatch",
			mutate: func(r *providerresolve.Resolved) {
				r.Locked.Version = "9.9.9"
			},
			wantErr: ErrIdentityMismatch,
		},
		{
			name: "checksum mismatch",
			mutate: func(r *providerresolve.Resolved) {
				r.Locked.Checksums[r.Platform] = "sha256:" + hex.EncodeToString(make([]byte, sha256.Size))
			},
			wantErr: registry.ErrChecksumMismatch,
		},
		{
			name: "no checksum for this platform",
			mutate: func(r *providerresolve.Resolved) {
				r.Platform = "plan9_arm"
			},
			wantErr: registry.ErrChecksumNotRecorded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A first, healthy provider so the assertion covers "already
			// launched providers are torn down", not just the failing one.
			first := toolFixture(t, "first")
			second := toolFixture(t, "second")
			tt.mutate(&second)

			s, launcher := startHarness(t, []providerresolve.Resolved{first, second})

			err := s.Start(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Start = %v, want %v", err, tt.wantErr)
			}

			// The failing provider's own subprocess is closed on its way
			// out, and the healthy one is torn down by the unwind.
			got := launcher.teardowns()
			if len(got) != 2 {
				t.Fatalf("teardowns = %v, want both the failing and the already-launched provider", got)
			}
			if got[0] != "second" || got[1] != "first" {
				t.Errorf("teardowns = %v, want [second first] — the failing provider closes itself, then the unwind reverses launch order", got)
			}
		})
	}
}

// TestStart_duplicateKeyUnwinds confirms a registration collision is a
// hard failure that unwinds, not a last-one-wins overwrite.
func TestStart_duplicateKeyUnwinds(t *testing.T) {
	first := toolFixture(t, "first")
	second := toolFixture(t, "second")
	s, launcher := startHarness(t, []providerresolve.Resolved{first, second})

	// Both resolve to the same in-process server, so both Describe as the
	// same {category, name}.
	err := s.Start(context.Background())
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Start = %v, want ErrDuplicateKey", err)
	}
	if got := launcher.teardowns(); len(got) != 2 {
		t.Errorf("teardowns = %v, want both providers torn down", got)
	}
}

// TestStart_launchFailureIsReturned covers the earliest failure point,
// before any RPC has been issued.
func TestStart_launchFailureIsReturned(t *testing.T) {
	boom := errors.New("spawn refused")
	s, launcher := startHarness(t, []providerresolve.Resolved{toolFixture(t, "fixture")})
	launcher.err = boom

	if err := s.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want the launch error", err)
	}
	if got := launcher.teardowns(); len(got) != 0 {
		t.Errorf("teardowns = %v, want none — nothing was ever launched", got)
	}
}

// TestStart_configDecodeFailureIsReturned covers step 6: a provider{}
// block whose value does not fit the schema the plugin advertised. This
// is the one failure that cannot be caught before a plugin is running —
// the ConfigSchema does not exist until it answers.
func TestStart_configDecodeFailureIsReturned(t *testing.T) {
	resolved := toolFixture(t, "fixture")
	s, launcher := startHarness(t, []providerresolve.Resolved{resolved})

	// fakeTool advertises one STRING attribute named after its category;
	// a list value cannot convert to it.
	s.cfg.ProviderBodies = map[string]hcl.Body{
		"fixture": parseHCLBody(t, commonv1.Category_CATEGORY_TOOL.String()+` = ["a", "b"]`),
	}

	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start with a wrong-typed provider attribute succeeded, want a decode error")
	}
	if got := launcher.teardowns(); len(got) != 1 {
		t.Errorf("teardowns = %v, want the launched provider torn down", got)
	}
}
