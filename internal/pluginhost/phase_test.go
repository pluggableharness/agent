package pluginhost

// Unit tier: the Prepare/Configure split, driven through the same launch
// seam start_test.go uses. What is asserted here is ordering, not
// transport — that a provider can be launched, described, and registered
// without having been configured, and that a category predicate decides
// which providers a given Configure pass reaches.

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/internal/providerresolve"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// phaseHarness wires a Supervisor over one tool provider and one
// frontend provider, each backed by its own in-process server so a test
// can tell which of the two a Configure pass actually reached.
//
// Both are dev overrides: this file is about phase ordering, and a dev
// override skips the checksum step that would otherwise need a real file
// on disk per provider.
func phaseHarness(t *testing.T) (s *Supervisor, toolCfg, frontendCfg *configured) {
	t.Helper()

	toolCfg, frontendCfg = &configured{}, &configured{}
	toolConn := dial(t, func(s *grpc.Server) {
		toolv1.RegisterToolServiceServer(s, &fakeTool{cfg: toolCfg})
	})
	frontendConn := dial(t, func(s *grpc.Server) {
		frontendv1.RegisterFrontendServiceServer(s, &fakeFrontend{cfg: frontendCfg})
	})
	clients := map[string]any{
		"toolp":     toolv1.NewToolServiceClient(toolConn),
		"frontendp": frontendv1.NewFrontendServiceClient(frontendConn),
	}

	cfg := testDeps(t)
	cfg.Resolved = []providerresolve.Resolved{
		{
			LocalName:      "toolp",
			Source:         "github.com/agentco/fake",
			Category:       commonv1.Category_CATEGORY_TOOL,
			BinaryPath:     "/dev/override/toolp",
			ViaDevOverride: true,
		},
		{
			LocalName:      "frontendp",
			Source:         "github.com/agentco/fake",
			Category:       commonv1.Category_CATEGORY_FRONTEND,
			BinaryPath:     "/dev/override/frontendp",
			ViaDevOverride: true,
		},
	}

	sup, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	sup.launch = func(_ context.Context, r providerresolve.Resolved, _ *callbackSlot) (*launchedPlugin, error) {
		return &launchedPlugin{
			client: clients[r.LocalName],
			close:  func(context.Context) error { return nil },
		}, nil
	}
	return sup, toolCfg, frontendCfg
}

// TestPrepare_registersWithoutConfiguring pins the window the split
// exists to create: after Prepare every provider is launched, described,
// and registered — so its real category is known and the provider
// catalog can be built over it — while none has been configured yet.
func TestPrepare_registersWithoutConfiguring(t *testing.T) {
	t.Parallel()

	s, toolCfg, frontendCfg := phaseHarness(t)

	if err := s.Prepare(t.Context()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, name := range []string{"toolp", "frontendp"} {
		if _, ok := s.cfg.Registry.ByLocalName(name); !ok {
			t.Errorf("ByLocalName(%q) reported ok = false after Prepare, want it registered", name)
		}
	}
	if toolCfg.got != nil {
		t.Error("Prepare configured the tool provider; Configure owns that step")
	}
	if frontendCfg.got != nil {
		t.Error("Prepare configured the frontend provider; Configure owns that step")
	}
}

// TestConfigure_honorsTheCategoryPredicate walks the exact two-pass
// sequence internal/kernel makes. The frontend staying unconfigured
// through the first pass is the whole point: it calls CreateSession from
// inside its own Configure handler, and the host answering that call
// does not exist until the kernel has built a catalog over the providers
// the first pass configured.
func TestConfigure_honorsTheCategoryPredicate(t *testing.T) {
	t.Parallel()

	s, toolCfg, frontendCfg := phaseHarness(t)
	ctx := t.Context()

	if err := s.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	notFrontend := func(c commonv1.Category) bool { return c != commonv1.Category_CATEGORY_FRONTEND }
	if err := s.Configure(ctx, notFrontend); err != nil {
		t.Fatalf("Configure(non-frontend): %v", err)
	}
	if toolCfg.got == nil {
		t.Error("the non-frontend pass did not configure the tool provider")
	}
	if frontendCfg.got != nil {
		t.Fatal("the non-frontend pass configured the frontend: it would call back into a kernel that does not exist yet")
	}

	onlyFrontend := func(c commonv1.Category) bool { return c == commonv1.Category_CATEGORY_FRONTEND }
	if err := s.Configure(ctx, onlyFrontend); err != nil {
		t.Fatalf("Configure(frontend): %v", err)
	}
	if frontendCfg.got == nil {
		t.Error("the frontend pass did not configure the frontend provider")
	}
}

// TestConfigure_skipsAlreadyConfigured asserts a provider is configured
// exactly once across passes. Without it, the kernel's second pass — or
// any later catch-all — would re-issue Configure to plugins that already
// have their config, which the category triple does not model as
// idempotent.
func TestConfigure_skipsAlreadyConfigured(t *testing.T) {
	t.Parallel()

	s, toolCfg, frontendCfg := phaseHarness(t)
	ctx := t.Context()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if toolCfg.got == nil || frontendCfg.got == nil {
		t.Fatal("Start left a provider unconfigured")
	}

	// Clearing the recorders makes a redundant Configure visible: a
	// second call would write them again.
	toolCfg.got, frontendCfg.got = nil, nil
	if err := s.Configure(ctx, nil); err != nil {
		t.Fatalf("Configure after Start: %v", err)
	}
	if toolCfg.got != nil {
		t.Error("the tool provider was configured twice")
	}
	if frontendCfg.got != nil {
		t.Error("the frontend provider was configured twice")
	}
}
