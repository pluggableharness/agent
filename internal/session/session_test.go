package session

import (
	"errors"
	"math"
	"testing"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/doomloop"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/providercatalog/drivers/fake"
	"github.com/pluggableharness/agent/internal/sessionscope"
	"github.com/pluggableharness/agent/internal/sessionstate"
	"github.com/pluggableharness/agent/internal/statebackend"
)

func TestNewRequiresCollaborators(t *testing.T) {
	t.Parallel()

	store, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	full := Config{
		Store:    store,
		Sessions: sessionstate.NewTable(),
		Scopes:   sessionscope.NewRegistry(),
		Bus:      eventbus.New(),
		Turn:     &scriptedTurn{},
		Hooks:    &recordingHooks{},
		Catalog:  fake.New(),
	}

	tests := []struct {
		name  string
		strip func(*Config)
		field string
	}{
		{"store", func(c *Config) { c.Store = nil }, "Store"},
		{"sessions", func(c *Config) { c.Sessions = nil }, "Sessions"},
		{"scopes", func(c *Config) { c.Scopes = nil }, "Scopes"},
		{"bus", func(c *Config) { c.Bus = nil }, "Bus"},
		{"turn", func(c *Config) { c.Turn = nil }, "Turn"},
		{"hooks", func(c *Config) { c.Hooks = nil }, "Hooks"},
		{"catalog", func(c *Config) { c.Catalog = nil }, "Catalog"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := full
			tt.strip(&cfg)
			_, err := New(cfg)
			if !errors.Is(err, ErrMissingCollaborator) {
				t.Fatalf("New: got %v, want ErrMissingCollaborator", err)
			}
			var me *missingError
			if !errors.As(err, &me) || me.Field != tt.field {
				t.Fatalf("New: got field %v, want %q", err, tt.field)
			}
		})
	}

	t.Run("complete config succeeds", func(t *testing.T) {
		t.Parallel()
		if _, err := New(full); err != nil {
			t.Fatalf("New: %v", err)
		}
	})
}

func TestMissingErrorMessage(t *testing.T) {
	t.Parallel()

	err := missing("Catalog")
	if got, want := err.Error(), "session: new: Config.Catalog is required"; got != want {
		t.Fatalf("Error: got %q, want %q", got, want)
	}
	if !errors.Is(err, ErrMissingCollaborator) {
		t.Fatal("missingError must unwrap to ErrMissingCollaborator")
	}
}

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, nil, false)
	if got, want := h.runner.doomLoop, doomloop.DefaultConfig; got != want {
		t.Fatalf("doom loop config: got %+v, want %+v", got, want)
	}
	if got, want := h.runner.kernelDefaultMaxDepth, math.MaxInt32; got != want {
		t.Fatalf("kernel default max depth: got %d, want %d", got, want)
	}
	if h.runner.logger == nil || h.runner.telem == nil {
		t.Fatal("logger and telemetry must default to non-nil")
	}
}

func TestNewRejectsInvalidDoomLoopConfig(t *testing.T) {
	t.Parallel()

	store, err := statebackend.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	_, err = New(Config{
		Store:    store,
		Sessions: sessionstate.NewTable(),
		Scopes:   sessionscope.NewRegistry(),
		Bus:      eventbus.New(),
		Turn:     &scriptedTurn{},
		Hooks:    &recordingHooks{},
		Catalog:  fake.New(),
		DoomLoop: doomloop.Config{WindowSize: 2, Threshold: 9},
	})
	if !errors.Is(err, doomloop.ErrInvalidThreshold) {
		t.Fatalf("New: got %v, want ErrInvalidThreshold", err)
	}
}

func TestBuiltinDefaultProfile(t *testing.T) {
	t.Parallel()

	profile := BuiltinDefaultProfile()
	if profile.Name != DefaultProfileName {
		t.Fatalf("name: got %q, want %q", profile.Name, DefaultProfileName)
	}
	if profile.MaxTurns != 200 || profile.MaxCostUSD != 5.00 || profile.MaxWallClockS != 3600 {
		t.Fatalf("bounds: got %d/%v/%d, want 200/5/3600", profile.MaxTurns, profile.MaxCostUSD, profile.MaxWallClockS)
	}
	if len(profile.Tools) != 0 || len(profile.SlashCommands) != 0 {
		t.Fatal("builtin default must inherit no tools and no slash commands")
	}
	if profile.MaxDepth != nil {
		t.Fatal("builtin default must leave MaxDepth unset so the kernel default applies")
	}

	profile.Tools = append(profile.Tools, "filesystem.*")
	if len(BuiltinDefaultProfile().Tools) != 0 {
		t.Fatal("BuiltinDefaultProfile must return a fresh value, not shared state")
	}
}

func TestResolveProfile(t *testing.T) {
	t.Parallel()

	configured := agentprofile.AgentProfile{Name: "code-reviewer", MaxTurns: 40}
	h := newHarness(t, map[string]agentprofile.AgentProfile{"code-reviewer": configured}, nil, false)

	tests := []struct {
		name     string
		in       string
		wantName string
		wantErr  error
	}{
		{"empty resolves to default", "", DefaultProfileName, nil},
		{"absent default falls back to builtin", DefaultProfileName, DefaultProfileName, nil},
		{"configured profile wins", "code-reviewer", "code-reviewer", nil},
		{"unknown profile errors", "nope", "", ErrUnknownProfile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			name, profile, err := h.runner.resolveProfile(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if name != tt.wantName {
				t.Fatalf("name: got %q, want %q", name, tt.wantName)
			}
			if tt.in == "code-reviewer" && profile.MaxTurns != 40 {
				t.Fatalf("configured profile not returned: %+v", profile)
			}
		})
	}
}

func TestResolveModelFallsBackToSoleLoadedModel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, nil, false)
	handle, err := h.runner.resolveModel(BuiltinDefaultProfile(), false)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if handle.Ref != testModelRef {
		t.Fatalf("ref: got %+v, want %+v", handle.Ref, testModelRef)
	}
}

func TestResolveModelAmbiguousDefaultErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		models int
	}{
		{"no models loaded", 0},
		{"two models loaded", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil, nil, false)
			h.catalog.Models = make(map[agentprofile.ModelRef]providercatalog.ModelHandle)
			for i := range tt.models {
				ref := agentprofile.ModelRef{Provider: "anthropic", ID: string(rune('a' + i))}
				h.catalog.AddModel(ref, providercatalog.ModelHandle{
					Producer: modelProducer(),
					Spec:     &modelv1.ModelSpec{Id: ref.ID, ContextWindow: 1000},
				})
			}
			_, err := h.runner.resolveModel(BuiltinDefaultProfile(), false)
			if !errors.Is(err, ErrNoDefaultModel) {
				t.Fatalf("resolveModel: got %v, want ErrNoDefaultModel", err)
			}
		})
	}
}

func TestResolveModelPropagatesSelectionFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, nil, false)
	profile := BuiltinDefaultProfile()
	profile.Model = agentprofile.ModelBlock{Primary: agentprofile.ModelRef{Provider: "openai", ID: "gpt"}}

	if _, err := h.runner.resolveModel(profile, false); !errors.Is(err, agentprofile.ErrNoEligibleModel) {
		t.Fatalf("resolveModel: got %v, want ErrNoEligibleModel", err)
	}
}

func TestResolveTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scoping []string
		want    []string
		wantErr error
	}{
		{"no scoping resolves to none", nil, nil, nil},
		{"concrete entry", []string{"filesystem.read_file"}, []string{"filesystem.read_file"}, nil},
		{"wildcard entry", []string{"filesystem.*"}, []string{"filesystem.read_file"}, nil},
		{"unloaded provider is a no-op", []string{"search.*"}, nil, nil},
		{"malformed entry errors", []string{"filesystem"}, nil, agentprofile.ErrMalformedToolScope},
		{"unknown tool errors", []string{"filesystem.write_file"}, nil, agentprofile.ErrUnknownTool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil, nil, true)
			profile := BuiltinDefaultProfile()
			profile.Tools = tt.scoping

			tools, err := h.runner.resolveTools(profile)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if len(tools) != len(tt.want) {
				t.Fatalf("tools: got %d entries (%v), want %v", len(tools), tools, tt.want)
			}
			for _, name := range tt.want {
				if _, ok := tools[name]; !ok {
					t.Fatalf("tools: missing %q", name)
				}
			}
		})
	}
}

func TestResolveGrantKeysDedupeAndOrder(t *testing.T) {
	t.Parallel()

	h := newHarness(t, profileWith(func(p *agentprofile.AgentProfile) {
		p.Tools = []string{"filesystem.*"}
	}), nil, true)

	res, err := h.runner.resolve(Spec{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []sessionscope.Key{
		sessionscope.KeyFor(modelProducer()),
		sessionscope.KeyFor(toolProducer()),
		sessionscope.KeyFor(contextProducer()),
	}
	if len(res.keys) != len(want) {
		t.Fatalf("keys: got %v, want %v", res.keys, want)
	}
	for i, key := range want {
		if res.keys[i] != key {
			t.Fatalf("keys[%d]: got %v, want %v", i, res.keys[i], key)
		}
	}
}

func TestResolveModelTargetReservesCeiling(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil, nil, false)
	res, err := h.runner.resolve(Spec{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got, want := res.target.GetContextWindow(), int64(200000); got != want {
		t.Fatalf("context window: got %d, want %d", got, want)
	}
	if got, want := res.target.GetEffectiveCeiling(), int64(160000); got != want {
		t.Fatalf("effective ceiling: got %d, want %d", got, want)
	}
	if got, want := res.target.GetId(), testModelRef.ID; got != want {
		t.Fatalf("id: got %q, want %q", got, want)
	}
}

func TestResolveLimitsAndDepthFromProfile(t *testing.T) {
	t.Parallel()

	depth := 3
	h := newHarness(t, profileWith(func(p *agentprofile.AgentProfile) {
		p.MaxTurns = 7
		p.MaxCostUSD = 1.25
		p.MaxWallClockS = 42
		p.MaxDepth = &depth
	}), nil, false)

	res, err := h.runner.resolve(Spec{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.limits.MaxTurns != 7 || res.limits.MaxCostUSD != 1.25 || res.limits.MaxWallClock.Seconds() != 42 {
		t.Fatalf("limits: got %+v", res.limits)
	}
	if res.remainingDepth != depth {
		t.Fatalf("remaining depth: got %d, want %d", res.remainingDepth, depth)
	}
}
