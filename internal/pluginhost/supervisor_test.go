package pluginhost

// Unit tier, in-package: reconcile, the callback slot, and Config
// validation are unexported or exercise unexported state, and none of
// them needs a subprocess. The launch/Describe/Configure sequence they
// support is covered by supervisor_integration_test.go against a real
// go-plugin subprocess.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"google.golang.org/grpc/metadata"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/kernelcallback"
	"github.com/pluggableharness/agent/internal/log"
	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/registry"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/telemetryrelay"
	"github.com/pluggableharness/agent/internal/tokencount"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeReadEventsStream is a minimal hand-written fake of
// kernelv1.KernelCallbackService_ReadEventsServer (go-testing.md: fakes,
// not mocking frameworks), mirroring
// internal/kernelcallback/events_test.go's fakeReadEventsStream — needed
// here because ReadEvents (once implemented, unlike its former
// codes.Unimplemented stub) calls stream.Context() unconditionally, so a
// nil stream argument is no longer a valid way to exercise it.
type fakeReadEventsStream struct {
	ctx context.Context
}

func (f *fakeReadEventsStream) Send(*kernelv1.StoredEvent) error { return nil }
func (f *fakeReadEventsStream) Context() context.Context         { return f.ctx }
func (f *fakeReadEventsStream) SetHeader(metadata.MD) error      { return nil }
func (f *fakeReadEventsStream) SendHeader(metadata.MD) error     { return nil }
func (f *fakeReadEventsStream) SetTrailer(metadata.MD)           {}
func (f *fakeReadEventsStream) SendMsg(any) error                { return nil }
func (f *fakeReadEventsStream) RecvMsg(any) error                { return nil }

// testDeps builds the process-wide singletons a valid Config needs.
func testDeps(t *testing.T) Config {
	t.Helper()

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

	logger := discardLogger()
	return Config{
		Registry:       NewRegistry(),
		Bus:            bus,
		Telemetry:      prov,
		TelemetryRelay: telemetryrelay.New(backend.RelayedSpans),
		Log:            log.NewServer(logger),
		Scopes:         sessionscope.NewRegistry(),
		Sessions:       sessionstate.NewTable(),
		Tokens:         tokencount.NewCounter(nil, prov, logger),
		Logger:         logger,
	}
}

func TestNewSupervisor_validation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr error
	}{
		{"missing registry", func(c *Config) { c.Registry = nil }, ErrMissingRegistry},
		{"missing bus", func(c *Config) { c.Bus = nil }, ErrMissingBus},
		{"missing telemetry", func(c *Config) { c.Telemetry = nil }, ErrMissingTelemetry},
		{"missing relay", func(c *Config) { c.TelemetryRelay = nil }, ErrMissingRelay},
		{"missing log", func(c *Config) { c.Log = nil }, ErrMissingLog},
		{"missing scopes", func(c *Config) { c.Scopes = nil }, ErrMissingScopes},
		{"missing sessions", func(c *Config) { c.Sessions = nil }, ErrMissingSessions},
		{"missing tokens", func(c *Config) { c.Tokens = nil }, ErrMissingTokens},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testDeps(t)
			tt.mutate(&cfg)
			s, err := NewSupervisor(cfg)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewSupervisor = %v, want %v", err, tt.wantErr)
			}
			if s != nil {
				t.Error("NewSupervisor returned a non-nil Supervisor alongside an error")
			}
		})
	}
}

func TestNewSupervisor_valid(t *testing.T) {
	cfg := testDeps(t)
	cfg.Logger = nil // exercises the documented slog.Default() fallback

	s, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if s.logger == nil {
		t.Error("supervisor logger is nil, want the slog.Default() fallback")
	}
}

// TestStart_noProviders confirms the empty case brings nothing up and
// still reports success — a bare agent.hcl with no required_providers is
// legal.
func TestStart_noProviders(t *testing.T) {
	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start with no providers: %v", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown with no providers: %v", err)
	}
}

// TestShutdown_idempotent confirms the documented "safe to call twice"
// contract, including after a Start that launched nothing.
func TestShutdown_idempotent(t *testing.T) {
	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	for i := range 3 {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown call %d: %v", i+1, err)
		}
	}
	if !s.shutDown {
		t.Error("supervisor did not record that it had shut down")
	}
}

// TestShutdown_afterCanceledContext confirms Shutdown does its work under
// context.WithoutCancel: the whole point is that shutdown is normally
// reached because the caller's ctx was already canceled.
func TestShutdown_afterCanceledContext(t *testing.T) {
	s, err := NewSupervisor(testDeps(t))
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A Live with no subprocess is skipped rather than dereferenced, so
	// this exercises the loop itself without a real plugin.
	s.launched = []*Live{{LocalName: "a", LaunchIndex: 0}}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with an already-canceled ctx: %v", err)
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	locked := registry.LockedProvider{Source: "github.com/agentco/p", Version: "1.2.3"}

	tests := []struct {
		name     string
		resolved providerresolve.Resolved
		producer *commonv1.ProducerRef
		wantErr  bool
	}{
		{
			name:     "dev override has no lock row to reconcile against",
			resolved: providerresolve.Resolved{ViaDevOverride: true},
			producer: &commonv1.ProducerRef{Source: "anything", Version: "9.9.9", Category: commonv1.Category_CATEGORY_TOOL},
		},
		{
			name:     "matching identity",
			resolved: providerresolve.Resolved{Locked: &locked, Category: commonv1.Category_CATEGORY_MODEL},
			producer: &commonv1.ProducerRef{Source: locked.Source, Version: locked.Version, Category: commonv1.Category_CATEGORY_MODEL},
		},
		{
			name:     "source mismatch",
			resolved: providerresolve.Resolved{Locked: &locked},
			producer: &commonv1.ProducerRef{Source: "github.com/evil/p", Version: locked.Version},
			wantErr:  true,
		},
		{
			name:     "version mismatch",
			resolved: providerresolve.Resolved{Locked: &locked},
			producer: &commonv1.ProducerRef{Source: locked.Source, Version: "9.9.9"},
			wantErr:  true,
		},
		{
			name:     "category mismatch",
			resolved: providerresolve.Resolved{Locked: &locked, Category: commonv1.Category_CATEGORY_MODEL},
			producer: &commonv1.ProducerRef{Source: locked.Source, Version: locked.Version, Category: commonv1.Category_CATEGORY_TOOL},
			wantErr:  true,
		},
		{
			name:     "unrecorded lock category is not checked",
			resolved: providerresolve.Resolved{Locked: &locked, Category: commonv1.Category_CATEGORY_UNSPECIFIED},
			producer: &commonv1.ProducerRef{Source: locked.Source, Version: locked.Version, Category: commonv1.Category_CATEGORY_TOOL},
		},
		{
			name:     "a plugin reporting no source or version is not a mismatch",
			resolved: providerresolve.Resolved{Locked: &locked, Category: commonv1.Category_CATEGORY_MODEL},
			producer: &commonv1.ProducerRef{Category: commonv1.Category_CATEGORY_MODEL},
		},
		{
			name: "published name is deliberately not compared to the local name",
			resolved: providerresolve.Resolved{
				LocalName: "anthropic",
				Locked:    &locked,
				Category:  commonv1.Category_CATEGORY_MODEL,
			},
			producer: &commonv1.ProducerRef{
				Name: "claude", Source: locked.Source, Version: locked.Version,
				Category: commonv1.Category_CATEGORY_MODEL,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := reconcile(tt.resolved, tt.producer)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("reconcile = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrIdentityMismatch) {
				t.Errorf("reconcile error = %v, want ErrIdentityMismatch", err)
			}
		})
	}
}

func TestProvisionalProducer(t *testing.T) {
	t.Parallel()

	got := provisionalProducer(providerresolve.Resolved{
		LocalName: "anthropic",
		Source:    "github.com/agentco/provider-anthropic",
		Version:   "1.2.3",
		Category:  commonv1.Category_CATEGORY_MODEL,
	})
	if got.GetName() != "anthropic" {
		t.Errorf("Name = %q, want the local name — nothing else is known pre-Describe", got.GetName())
	}
	if got.GetVersion() != "1.2.3" || got.GetSource() != "github.com/agentco/provider-anthropic" {
		t.Errorf("provisional producer = %v, want the resolved source and version", got)
	}
	if got.GetCategory() != commonv1.Category_CATEGORY_MODEL {
		t.Errorf("Category = %v, want CATEGORY_MODEL", got.GetCategory())
	}
}

func TestCategoryText(t *testing.T) {
	t.Parallel()

	if got := categoryText(commonv1.Category_CATEGORY_UNSPECIFIED); got != "" {
		t.Errorf("categoryText(UNSPECIFIED) = %q, want empty", got)
	}
	if got := categoryText(commonv1.Category_CATEGORY_SLASHCOMMAND); got != "slashcommand" {
		t.Errorf("categoryText(SLASHCOMMAND) = %q, want %q", got, "slashcommand")
	}
}

func TestProviderBody(t *testing.T) {
	t.Parallel()

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL([]byte("api_key = \"x\"\n"), "agent.hcl")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}

	s := &Supervisor{cfg: Config{ProviderBodies: map[string]hcl.Body{
		"declared": file.Body,
		"nilbody":  nil,
	}}}

	if got := s.providerBody("declared"); got != file.Body {
		t.Error("providerBody(declared) did not return the declared body")
	}
	for _, name := range []string{"nilbody", "absent"} {
		body := s.providerBody(name)
		if body == nil {
			t.Fatalf("providerBody(%s) = nil, want an empty body", name)
		}
		attrs, diags := body.JustAttributes()
		if diags.HasErrors() || len(attrs) != 0 {
			t.Errorf("providerBody(%s) = %v (%v), want an empty body", name, attrs, diags)
		}
	}
}

func TestCallbackSlot_forwardsToInstalledServer(t *testing.T) {
	t.Parallel()

	logger := discardLogger()
	first := kernelcallback.NewServer(kernelcallback.Config{
		Log:       log.NewServer(logger),
		Producer:  &commonv1.ProducerRef{Name: "provisional", Category: commonv1.Category_CATEGORY_TOOL},
		Telemetry: mustTelemetry(t),
		Logger:    logger,
	})
	slot := newCallbackSlot(first)

	if slot.server() != first {
		t.Fatal("newCallbackSlot did not install its initial server")
	}

	// GetConfig is the RPC the slot exists for: with no resolved config
	// installed it answers empty, and with one it answers that one — the
	// swap a plugin calling GetConfig from inside Configure depends on.
	got, err := slot.GetConfig(context.Background(), &kernelv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig against the provisional server: %v", err)
	}
	if len(got.GetConfig().GetFields()) != 0 {
		t.Errorf("GetConfig = %v, want an empty struct before a real config is installed", got.GetConfig())
	}

	resolved := mustStruct(t, map[string]any{"api_key": "value"})
	second := kernelcallback.NewServer(kernelcallback.Config{
		Log:            log.NewServer(logger),
		Producer:       &commonv1.ProducerRef{Name: "real", Category: commonv1.Category_CATEGORY_TOOL},
		Telemetry:      mustTelemetry(t),
		ResolvedConfig: resolved,
		Logger:         logger,
	})
	slot.set(second)

	if slot.server() != second {
		t.Fatal("set did not install the new server")
	}
	got, err = slot.GetConfig(context.Background(), &kernelv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig after set: %v", err)
	}
	if got.GetConfig().GetFields()["api_key"].GetStringValue() != "value" {
		t.Errorf("GetConfig = %v, want the installed resolved config", got.GetConfig())
	}
}

// TestCallbackSlot_forwardsEveryRPC confirms no RPC silently falls
// through to the embedded Unimplemented server — every method must reach
// the installed kernelcallback.Server, whose own answer (real result or
// its own documented codes.Unimplemented stub) is what the plugin sees.
func TestCallbackSlot_forwardsEveryRPC(t *testing.T) {
	t.Parallel()

	logger := discardLogger()
	prov := mustTelemetry(t)
	slot := newCallbackSlot(kernelcallback.NewServer(kernelcallback.Config{
		Log:       log.NewServer(logger),
		Producer:  &commonv1.ProducerRef{Name: "p", Category: commonv1.Category_CATEGORY_TOOL},
		Telemetry: prov,
		Scopes:    sessionscope.NewRegistry(),
		Sessions:  sessionstate.NewTable(),
		Tokens:    tokencount.NewCounter(nil, prov, logger),
		Logger:    logger,
	}))

	ctx := context.Background()

	// The RPCs internal/kernelcallback really implements must succeed
	// through the slot rather than returning Unimplemented.
	if _, err := slot.GetTelemetryConfig(ctx, &kernelv1.GetTelemetryConfigRequest{}); err != nil {
		t.Errorf("GetTelemetryConfig through the slot: %v", err)
	}
	if _, err := slot.GetConfig(ctx, &kernelv1.GetConfigRequest{}); err != nil {
		t.Errorf("GetConfig through the slot: %v", err)
	}

	// Every remaining RPC must surface internal/kernelcallback's own
	// error — its Unimplemented stub for the ones it does not implement,
	// its own request validation for the ones it does — proving the call
	// reached that package rather than the embedded
	// UnimplementedKernelCallbackServiceServer this slot also carries.
	for _, tc := range []struct {
		name string
		// want is the message prefix proving which kernel-side package
		// answered. Log is the one RPC internal/kernelcallback delegates
		// straight through to internal/log, so its error is that
		// package's, which is itself the proof the forward happened.
		want string
		call func() error
	}{
		{"RunSession", "kernelcallback:", func() error { _, err := slot.RunSession(ctx, &kernelv1.RunSessionRequest{}); return err }},
		{"CountTokens", "kernelcallback:", func() error { _, err := slot.CountTokens(ctx, &kernelv1.CountTokensRequest{}); return err }},
		{"Emit", "kernelcallback:", func() error { _, err := slot.Emit(ctx, &kernelv1.EmitRequest{}); return err }},
		{"GetSession", "kernelcallback:", func() error { _, err := slot.GetSession(ctx, &kernelv1.GetSessionRequest{}); return err }},
		{"ReadEvents", "kernelcallback:", func() error {
			// ReadEvents is server-streaming: its context comes from the
			// stream argument (stream.Context()), not a direct ctx
			// parameter, so contextcheck can't see that ctx does flow
			// through via fakeReadEventsStream.ctx below.
			return slot.ReadEvents(&kernelv1.ReadEventsRequest{}, &fakeReadEventsStream{ctx: ctx}) //nolint:contextcheck // ctx flows via the stream, see comment above
		}},
		{"ExportSpans", "kernelcallback:", func() error { _, err := slot.ExportSpans(ctx, &kernelv1.ExportSpansRequest{}); return err }},
		{"RecordMetrics", "kernelcallback:", func() error { _, err := slot.RecordMetrics(ctx, &kernelv1.RecordMetricsRequest{}); return err }},
		{"Publish", "kernelcallback:", func() error { _, err := slot.Publish(ctx, &kernelv1.PublishRequest{}); return err }},
		{"Log", "log:", func() error { _, err := slot.Log(ctx, &kernelv1.LogRequest{}); return err }},
	} {
		err := tc.call()
		if err == nil {
			t.Errorf("%s through the slot = nil error, want the kernel-side implementation's own error", tc.name)
			continue
		}
		if got := err.Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%s through the slot = %q, want it to contain %q (the slot must not answer for it)", tc.name, got, tc.want)
		}
	}

	// Subscribe is exercised by the integration tier instead: its handler
	// reads the stream's context immediately, so there is no nil stream
	// to call it with here (confirmed: internal/kernelcallback's
	// eventbus.go dereferences it on the first line).
}
