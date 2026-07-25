//go:build integration

package pluginhost_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/plugincache"
	"github.com/pluggableharness/agent/internal/pluginhost"
	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/registry"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	"github.com/pluggableharness/agent/internal/tokencount"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// Mirrors of the fixture's own constants and default identity
// (testdata/plugin/main.go).
const (
	fixtureConfigAttr        = "greeting"
	configureLogMessage      = "fixture configure saw its own config"
	configMismatchLogMessage = "fixture configure config mismatch"
)

// fixtureBuild describes one built fixture binary and the identity it
// reports through Describe.
type fixtureBuild struct {
	path    string
	name    string
	version string
	source  string
}

// alpha and beta are two fixture binaries built from the same source
// with distinct linker-injected identities, so two of them can be
// brought up in one Supervisor without colliding on {category, name}.
var alpha, beta fixtureBuild

// TestMain delegates to run so every cleanup happens before os.Exit,
// which skips deferred calls (go-style.md).
func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "pluginhost-fixture-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pluginhost: integration: mkdtemp:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	alpha = fixtureBuild{
		path:    filepath.Join(dir, "fixture-alpha"),
		name:    "alpha",
		version: "1.0.0",
		source:  "github.com/agentco/pluginhost-fixture-alpha",
	}
	beta = fixtureBuild{
		path:    filepath.Join(dir, "fixture-beta"),
		name:    "beta",
		version: "2.0.0",
		source:  "github.com/agentco/pluginhost-fixture-beta",
	}

	for _, f := range []fixtureBuild{alpha, beta} {
		ldflags := fmt.Sprintf("-X main.fixtureName=%s -X main.fixtureVersion=%s -X main.fixtureSource=%s",
			f.name, f.version, f.source)
		cmd := exec.CommandContext(context.Background(), "go", "build",
			"-tags=integration", "-ldflags", ldflags, "-o", f.path, "./testdata/plugin")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "pluginhost: integration: build fixture %s: %v\n%s", f.name, err, out)
			return 1
		}
	}

	return m.Run()
}

// captureHandler is a hand-written, concurrency-safe slog.Handler fake
// (go-testing.md: fakes, not mocking frameworks). Concurrency-safe
// because a plugin's Log callbacks arrive on gRPC handler goroutines,
// concurrently with the test's own assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// count reports how many records carrying msg have arrived.
func (h *captureHandler) count(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.records {
		if r.Message == msg {
			n++
		}
	}
	return n
}

// waitFor blocks until at least n records carrying msg have arrived, or
// fails the test. A plugin's callbacks arrive asynchronously on its
// side, so polling is required rather than assuming synchronous
// delivery; the bound sits well inside the 5s integration budget.
func waitFor(t *testing.T, h *captureHandler, msg string, n int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for h.count(msg) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%q reached the kernel's log server %d time(s), want %d", msg, h.count(msg), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// harness bundles a Supervisor under test with the pieces a test asserts
// against.
type harness struct {
	supervisor *pluginhost.Supervisor
	registry   *pluginhost.Registry
	logs       *captureHandler
}

// newHarness builds a Supervisor over resolved.
func newHarness(t *testing.T, resolved []providerresolve.Resolved, bodies map[string]hcl.Body) *harness {
	t.Helper()

	h := &captureHandler{}
	logger := slog.New(h)

	backend := fake.New()
	prov, err := telemetry.New(context.Background(), telemetry.DefaultConfig, backend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("telemetry.Shutdown: %v", err)
		}
	})

	bus := eventbus.New()
	t.Cleanup(func() { _ = bus.Close() })

	reg := pluginhost.NewRegistry()
	// The three registries a per-plugin kernel-callback server resolves
	// its caller's session through. Config requires all three; a real
	// composition root shares one set process-wide (internal/kernel).
	scopes := sessionscope.NewRegistry()
	sessions := sessionstate.NewTable()

	s, err := pluginhost.NewSupervisor(pluginhost.Config{
		Resolved:       resolved,
		Registry:       reg,
		Bus:            bus,
		Telemetry:      prov,
		TelemetryRelay: telemetryrelay.New(backend.RelayedSpans),
		Log:            log.NewServer(logger),
		Scopes:         scopes,
		Sessions:       sessions,
		Tokens:         tokencount.NewCounter(reg, prov, logger),
		ProviderBodies: bodies,
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown (cleanup): %v", err)
		}
	})

	return &harness{supervisor: s, registry: reg, logs: h}
}

// parseBody parses one provider{} block body from an HCL fragment.
func parseBody(t *testing.T, src string) hcl.Body {
	t.Helper()

	file, diags := hclparse.NewParser().ParseHCL([]byte(src), "agent.hcl")
	if diags.HasErrors() {
		t.Fatalf("parse %q: %v", src, diags)
	}
	return file.Body
}

// cached copies f's binary into a real plugin-cache layout and returns a
// Resolved pointing at it, with a lock row whose checksum is the copy's
// actual digest — so the supervisor's VerifyChecksum step is exercised
// for real rather than skipped.
func cached(t *testing.T, f fixtureBuild, localName string) providerresolve.Resolved {
	t.Helper()

	platform := plugincache.Platform()
	cacheDir := t.TempDir()
	path := plugincache.BinaryPath(cacheDir, f.source, f.version, platform)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatalf("read fixture binary: %v", err)
	}
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatalf("write fixture copy: %v", err)
	}

	sum := sha256.Sum256(data)
	locked := registry.LockedProvider{
		Source:    f.source,
		Version:   f.version,
		Category:  "tool",
		Checksums: map[string]string{platform: "sha256:" + hex.EncodeToString(sum[:])},
	}
	return providerresolve.Resolved{
		LocalName:  localName,
		Source:     f.source,
		Version:    f.version,
		Category:   commonv1.Category_CATEGORY_TOOL,
		BinaryPath: path,
		Platform:   platform,
		Locked:     &locked,
	}
}

// devOverride returns a Resolved shaped like a dev_overrides entry: an
// unknown category and no lock row, which is what forces the
// supervisor's category probe.
func devOverride(f fixtureBuild, localName string) providerresolve.Resolved {
	return providerresolve.Resolved{
		LocalName:      localName,
		Source:         f.source,
		Category:       commonv1.Category_CATEGORY_UNSPECIFIED,
		BinaryPath:     f.path,
		ViaDevOverride: true,
	}
}

// TestSupervisor_startBringsUpAndConfigures is the primary assertion:
// the whole per-provider sequence runs against a real subprocess, and
// the fixture's Configure confirms GetConfig already answered with its
// decoded config — the ordering guarantee callbackSlot exists for.
func TestSupervisor_startBringsUpAndConfigures(t *testing.T) {
	resolved := cached(t, alpha, "fixture-tool")
	h := newHarness(t, []providerresolve.Resolved{resolved}, map[string]hcl.Body{
		"fixture-tool": parseBody(t, `greeting = "hello"`),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.supervisor.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if h.logs.count(configMismatchLogMessage) > 0 {
		t.Fatal("the fixture's Configure saw a different config from GetConfig — the decoded config was not installed before Configure")
	}
	waitFor(t, h.logs, configureLogMessage, 1)

	live, ok := h.registry.ByLocalName("fixture-tool")
	if !ok {
		t.Fatal("ByLocalName(fixture-tool) reported ok = false after a successful Start")
	}
	if live.Producer.GetName() != alpha.name {
		t.Errorf("Producer.Name = %q, want the plugin's own published name %q", live.Producer.GetName(), alpha.name)
	}
	if live.Producer.GetCategory() != commonv1.Category_CATEGORY_TOOL {
		t.Errorf("Producer.Category = %v, want CATEGORY_TOOL", live.Producer.GetCategory())
	}
	if live.LaunchIndex != 0 {
		t.Errorf("LaunchIndex = %d, want 0", live.LaunchIndex)
	}
	if live.ConfigSchema == nil || len(live.ConfigSchema.GetAttributes()) != 1 {
		t.Fatalf("ConfigSchema = %v, want the fixture's one declared attribute", live.ConfigSchema)
	}
	if got := live.ConfigSchema.GetAttributes()[0].GetName(); got != fixtureConfigAttr {
		t.Errorf("ConfigSchema attribute = %q, want %q", got, fixtureConfigAttr)
	}
	if _, ok := live.Capabilities.(*toolv1.GetSchemaResponse); !ok {
		t.Errorf("Capabilities = %T, want the whole *toolv1.GetSchemaResponse", live.Capabilities)
	}

	// Registered under the described identity, not the local name.
	if _, ok := h.registry.ByKey(pluginhost.Key{Category: commonv1.Category_CATEGORY_TOOL, Name: alpha.name}); !ok {
		t.Error("ByKey(tool/alpha) reported ok = false; registration must key on the described identity")
	}
	if _, ok := h.registry.ByKey(pluginhost.Key{Category: commonv1.Category_CATEGORY_TOOL, Name: "fixture-tool"}); ok {
		t.Error("ByKey resolved the agent.hcl local name; it must key on the described identity")
	}

	// The category client and the hook client both work, over the one
	// shared connection.
	client, ok := live.ToolClient()
	if !ok {
		t.Fatalf("ToolClient() reported ok = false for a %T", live.Client)
	}
	resp, err := client.GetSchema(ctx, &toolv1.GetSchemaRequest{})
	if err != nil {
		t.Fatalf("GetSchema over the registered client: %v", err)
	}
	if len(resp.GetTools()) != 1 {
		t.Errorf("GetSchema returned %d tools, want 1", len(resp.GetTools()))
	}
	if _, ok := live.HookClient(); !ok {
		t.Error("HookClient() reported ok = false for a launched plugin")
	}
}

// TestSupervisor_launchOrderAndShutdown brings two real providers up and
// asserts the ordering contract: launch order is the resolved order,
// LaunchIndex records it, and after Shutdown neither subprocess answers.
func TestSupervisor_launchOrderAndShutdown(t *testing.T) {
	first := cached(t, alpha, "first")
	second := cached(t, beta, "second")
	h := newHarness(t, []providerresolve.Resolved{first, second}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.supervisor.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := make([]string, 0, 2)
	for i, live := range h.registry.All() {
		got = append(got, live.LocalName)
		if live.LaunchIndex != i {
			t.Errorf("%s LaunchIndex = %d, want %d", live.LocalName, live.LaunchIndex, i)
		}
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("registry order = %v, want the resolved order %v", got, want)
	}

	clients := make([]toolv1.ToolServiceClient, 0, 2)
	for _, live := range h.registry.All() {
		c, ok := live.ToolClient()
		if !ok {
			t.Fatalf("%s: ToolClient() reported ok = false", live.LocalName)
		}
		clients = append(clients, c)
	}

	if err := h.supervisor.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()
	for i, c := range clients {
		if _, err := c.GetSchema(rpcCtx, &toolv1.GetSchemaRequest{}); err == nil {
			t.Errorf("plugin %d answered GetSchema after Shutdown, want its subprocess gone", i)
		}
	}
}

// TestSupervisor_duplicateKeyIsFatal launches the same binary twice under
// two local names: v1 has no aliasing mechanism, so the second
// registration is a hard error and the whole Start unwinds.
func TestSupervisor_duplicateKeyIsFatal(t *testing.T) {
	first := cached(t, alpha, "first")
	second := cached(t, alpha, "second")
	h := newHarness(t, []providerresolve.Resolved{first, second}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := h.supervisor.Start(ctx)
	if !errors.Is(err, pluginhost.ErrDuplicateKey) {
		t.Fatalf("Start = %v, want ErrDuplicateKey", err)
	}

	// All-or-nothing teardown: the first plugin did come up and is still
	// in the registry (Add succeeded for it), but its subprocess must
	// have been torn down again rather than left running.
	live, ok := h.registry.ByLocalName("first")
	if !ok {
		t.Fatal("the first provider is missing from the registry")
	}
	client, ok := live.ToolClient()
	if !ok {
		t.Fatal("ToolClient() reported ok = false")
	}
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()
	if _, err := client.GetSchema(rpcCtx, &toolv1.GetSchemaRequest{}); err == nil {
		t.Error("the first plugin still answers after a failed Start, want it torn down")
	}
}

// TestSupervisor_devOverrideCategoryProbe confirms a provider with no
// recorded category is probed: the fixture serves only tool, and the
// probe order tries model first, so a successful bring-up proves the
// probe skipped a category this plugin does not serve rather than
// latching onto the first one it tried.
func TestSupervisor_devOverrideCategoryProbe(t *testing.T) {
	h := newHarness(t, []providerresolve.Resolved{devOverride(alpha, "dev")}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := h.supervisor.Start(ctx); err != nil {
		t.Fatalf("Start with a dev-override provider: %v", err)
	}

	live, ok := h.registry.ByLocalName("dev")
	if !ok {
		t.Fatal("ByLocalName(dev) reported ok = false after a successful probe")
	}
	if live.Producer.GetCategory() != commonv1.Category_CATEGORY_TOOL {
		t.Errorf("probed category = %v, want CATEGORY_TOOL", live.Producer.GetCategory())
	}
	if _, ok := live.ToolClient(); !ok {
		t.Errorf("ToolClient() reported ok = false for a probed tool plugin (%T)", live.Client)
	}
}

// TestSupervisor_identityMismatchIsFatal confirms a binary contradicting
// its lock row fails startup rather than being launched anyway: the lock
// file is the source of truth for what is allowed to run.
func TestSupervisor_identityMismatchIsFatal(t *testing.T) {
	// beta's binary, cached and locked under alpha's version.
	resolved := cached(t, beta, "fixture-tool")
	resolved.Locked.Version = alpha.version

	h := newHarness(t, []providerresolve.Resolved{resolved}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := h.supervisor.Start(ctx)
	if !errors.Is(err, pluginhost.ErrIdentityMismatch) {
		t.Fatalf("Start = %v, want ErrIdentityMismatch", err)
	}
	if len(h.registry.All()) != 0 {
		t.Error("a plugin whose identity contradicts the lock file was registered anyway")
	}
}

// TestSupervisor_checksumMismatchIsFatal confirms a tampered binary fails
// startup even though it launches and describes itself correctly.
func TestSupervisor_checksumMismatchIsFatal(t *testing.T) {
	resolved := cached(t, alpha, "fixture-tool")
	resolved.Locked.Checksums[resolved.Platform] = "sha256:" + hex.EncodeToString(make([]byte, sha256.Size))

	h := newHarness(t, []providerresolve.Resolved{resolved}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.supervisor.Start(ctx); err == nil {
		t.Fatal("Start with a checksum mismatch succeeded, want an error")
	}
	if len(h.registry.All()) != 0 {
		t.Error("a plugin failing checksum verification was registered anyway")
	}
}

// TestSupervisor_shutdownIdempotentUnderCanceledContext closes the loop
// on the "safe to call twice" contract against a genuinely running
// subprocess, with the caller's context already canceled — which is how
// shutdown is normally reached, and what context.WithoutCancel protects
// the drain window from.
func TestSupervisor_shutdownIdempotentUnderCanceledContext(t *testing.T) {
	h := newHarness(t, []providerresolve.Resolved{cached(t, alpha, "fixture-tool")}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.supervisor.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	shutdownCancel()
	for i := range 2 {
		if err := h.supervisor.Shutdown(shutdownCtx); err != nil {
			t.Fatalf("Shutdown call %d: %v", i+1, err)
		}
	}
}

// TestSupervisor_absentProviderBlockConfiguresWithEmptyConfig confirms a
// provider declared in required_providers but never given a provider{}
// block is configured with an empty config rather than failing — the
// ordinary case for a provider that takes none.
func TestSupervisor_absentProviderBlockConfiguresWithEmptyConfig(t *testing.T) {
	h := newHarness(t, []providerresolve.Resolved{cached(t, alpha, "fixture-tool")}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.supervisor.Start(ctx); err != nil {
		t.Fatalf("Start with no provider{} block: %v", err)
	}
	waitFor(t, h.logs, configureLogMessage, 1)
	if h.logs.count(configMismatchLogMessage) > 0 {
		t.Error("Configure and GetConfig disagreed for a provider with no declared config")
	}
}
