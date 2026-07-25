package fake_test

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/providercatalog/drivers/fake"
)

// The four stub clients exist only so a test can assert a handle's
// Client field round-trips by identity. Embedding the generated client
// interface satisfies it without hand-writing every RPC method; calling
// one panics, which is correct — this fake never invokes an RPC.
type stubModelClient struct{ modelv1.ModelServiceClient }

type stubToolClient struct{ toolv1.ToolServiceClient }

type stubContextClient struct{ contextv1.ContextServiceClient }

type stubHookClient struct {
	hookv1.HookSubscriberServiceClient
}

var _ providercatalog.Catalog = (*fake.Catalog)(nil)

func TestCatalogModel(t *testing.T) {
	t.Parallel()

	ref := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"}
	spec := &modelv1.ModelSpec{Id: "claude-opus-4", ContextWindow: 200_000}
	client := &stubModelClient{}
	producer := &commonv1.ProducerRef{Name: "anthropic", Category: commonv1.Category_CATEGORY_MODEL}

	cat := fake.New().AddModel(ref, providercatalog.ModelHandle{
		Producer: producer,
		Spec:     spec,
		Client:   client,
	})

	tests := []struct {
		name    string
		ref     agentprofile.ModelRef
		wantErr bool
	}{
		{name: "registered", ref: ref},
		{name: "unknown provider", ref: agentprofile.ModelRef{Provider: "openai", ID: "claude-opus-4"}, wantErr: true},
		{name: "unknown id", ref: agentprofile.ModelRef{Provider: "anthropic", ID: "claude-sonnet-4"}, wantErr: true},
		{name: "zero ref", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cat.Model(tt.ref)
			if tt.wantErr {
				if !errors.Is(err, providercatalog.ErrNotFound) {
					t.Fatalf("Model(%+v): want ErrNotFound, got %v", tt.ref, err)
				}
				if got != (providercatalog.ModelHandle{}) {
					t.Errorf("Model(%+v): want zero handle on error, got %+v", tt.ref, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Model(%+v): unexpected error: %v", tt.ref, err)
			}
			if got.Ref != ref {
				t.Errorf("Model: Ref = %+v, want %+v (AddModel must stamp the key)", got.Ref, ref)
			}
			if got.Spec != spec {
				t.Errorf("Model: Spec = %p, want %p", got.Spec, spec)
			}
			if got.Client != modelv1.ModelServiceClient(client) {
				t.Errorf("Model: Client = %v, want the registered stub", got.Client)
			}
			if got.Producer != producer {
				t.Errorf("Model: Producer = %p, want %p", got.Producer, producer)
			}
		})
	}
}

func TestCatalogTool(t *testing.T) {
	t.Parallel()

	schema := &toolv1.ToolSchema{
		Name:           "read_file",
		Kind:           toolv1.ToolKind_TOOL_KIND_DATA_SOURCE,
		TerminatesTurn: false,
	}
	client := &stubToolClient{}

	cat := fake.New().
		AddTool("fs", "read_file", providercatalog.ToolHandle{
			Schema:          schema,
			Client:          client,
			SupportsPreview: true,
		}).
		AddTool("fs", "write_file", providercatalog.ToolHandle{}).
		AddTool("task", "finish", providercatalog.ToolHandle{TerminatesTurn: true})

	tests := []struct {
		name     string
		provider string
		tool     string
		wantErr  bool
	}{
		{name: "registered", provider: "fs", tool: "read_file"},
		{name: "same provider other tool", provider: "fs", tool: "write_file"},
		{name: "unknown provider", provider: "shell", tool: "read_file", wantErr: true},
		{name: "unknown tool", provider: "fs", tool: "delete_file", wantErr: true},
		{name: "halves swapped", provider: "read_file", tool: "fs", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cat.Tool(tt.provider, tt.tool)
			if tt.wantErr {
				if !errors.Is(err, providercatalog.ErrNotFound) {
					t.Fatalf("Tool(%q, %q): want ErrNotFound, got %v", tt.provider, tt.tool, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Tool(%q, %q): unexpected error: %v", tt.provider, tt.tool, err)
			}
			if got.Provider != tt.provider {
				t.Errorf("Tool: Provider = %q, want %q (AddTool must stamp the key)", got.Provider, tt.provider)
			}
		})
	}

	t.Run("fields round-trip", func(t *testing.T) {
		t.Parallel()

		got, err := cat.Tool("fs", "read_file")
		if err != nil {
			t.Fatalf("Tool: unexpected error: %v", err)
		}
		if got.Schema != schema {
			t.Errorf("Tool: Schema = %p, want %p", got.Schema, schema)
		}
		if got.Client != toolv1.ToolServiceClient(client) {
			t.Errorf("Tool: Client = %v, want the registered stub", got.Client)
		}
		if !got.SupportsPreview {
			t.Error("Tool: SupportsPreview = false, want true")
		}
		if got.TerminatesTurn {
			t.Error("Tool: TerminatesTurn = true, want false")
		}

		terminal, err := cat.Tool("task", "finish")
		if err != nil {
			t.Fatalf("Tool(task, finish): unexpected error: %v", err)
		}
		if !terminal.TerminatesTurn {
			t.Error("Tool(task, finish): TerminatesTurn = false, want true")
		}
	})
}

func TestCatalogHook(t *testing.T) {
	t.Parallel()

	client := &stubHookClient{}
	points := []commonv1.HookPoint{
		commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL,
		commonv1.HookPoint_HOOK_POINT_POST_TOOL_CALL,
	}

	cat := fake.New().AddHook("fs", providercatalog.HookHandle{
		Producer:        &commonv1.ProducerRef{Name: "filesystem", Category: commonv1.Category_CATEGORY_TOOL},
		Client:          client,
		SupportedPoints: points,
	})

	got, err := cat.Hook("fs")
	if err != nil {
		t.Fatalf("Hook(fs): unexpected error: %v", err)
	}
	if got.Client != hookv1.HookSubscriberServiceClient(client) {
		t.Errorf("Hook: Client = %v, want the registered stub", got.Client)
	}
	if !slices.Equal(got.SupportedPoints, points) {
		t.Errorf("Hook: SupportedPoints = %v, want %v", got.SupportedPoints, points)
	}
	// The local name is the agent.hcl one, not the producer's published
	// name — looking up the latter must miss.
	if _, err := cat.Hook("filesystem"); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Fatalf("Hook(filesystem): want ErrNotFound, got %v", err)
	}
	if _, err := cat.Hook(""); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Fatalf(`Hook(""): want ErrNotFound, got %v`, err)
	}
}

func TestCatalogContexts(t *testing.T) {
	t.Parallel()

	t.Run("add stamps declaration order", func(t *testing.T) {
		t.Parallel()

		caps := &contextv1.ContextCapabilities{DefaultTokenBudget: 4096}
		client := &stubContextClient{}
		cat := fake.New().
			AddContext(providercatalog.ContextHandle{Provider: "system_prompt", Capabilities: caps, Client: client, TokenBudget: 4096}).
			AddContext(providercatalog.ContextHandle{Provider: "git_status"}).
			AddContext(providercatalog.ContextHandle{Provider: "claude_md"})

		got := cat.Contexts()
		wantNames := []string{"system_prompt", "git_status", "claude_md"}
		if len(got) != len(wantNames) {
			t.Fatalf("Contexts: len = %d, want %d", len(got), len(wantNames))
		}
		for i, want := range wantNames {
			if got[i].Provider != want {
				t.Errorf("Contexts[%d].Provider = %q, want %q", i, got[i].Provider, want)
			}
			if got[i].Position != i {
				t.Errorf("Contexts[%d].Position = %d, want %d", i, got[i].Position, i)
			}
		}
		if got[0].Capabilities != caps {
			t.Errorf("Contexts[0].Capabilities = %p, want %p", got[0].Capabilities, caps)
		}
		if got[0].Client != contextv1.ContextServiceClient(client) {
			t.Errorf("Contexts[0].Client = %v, want the registered stub", got[0].Client)
		}
		if got[0].TokenBudget != 4096 {
			t.Errorf("Contexts[0].TokenBudget = %d, want 4096", got[0].TokenBudget)
		}
	})

	t.Run("literal positions win over slice order", func(t *testing.T) {
		t.Parallel()

		cat := &fake.Catalog{ContextProviders: []providercatalog.ContextHandle{
			{Provider: "third", Position: 7},
			{Provider: "first", Position: 0},
			{Provider: "second", Position: 3},
		}}

		got := cat.Contexts()
		want := []string{"first", "second", "third"}
		for i, name := range want {
			if got[i].Provider != name {
				t.Errorf("Contexts[%d].Provider = %q, want %q", i, got[i].Provider, name)
			}
		}
	})

	t.Run("returned slice is a copy", func(t *testing.T) {
		t.Parallel()

		cat := fake.New().AddContext(providercatalog.ContextHandle{Provider: "git_status"})
		got := cat.Contexts()
		got[0].Provider = "mutated"

		if again := cat.Contexts(); again[0].Provider != "git_status" {
			t.Errorf("Contexts: caller mutation leaked into the catalog: %q", again[0].Provider)
		}
	})
}

func TestCatalogModelSpecs(t *testing.T) {
	t.Parallel()

	opus := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"}
	haiku := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-haiku-4"}
	opusSpec := &modelv1.ModelSpec{Id: opus.ID, ContextWindow: 200_000}
	haikuSpec := &modelv1.ModelSpec{Id: haiku.ID, ContextWindow: 100_000}

	cat := fake.New().
		AddModel(opus, providercatalog.ModelHandle{Spec: opusSpec}).
		AddModel(haiku, providercatalog.ModelHandle{Spec: haikuSpec})

	specs := cat.ModelSpecs()
	if len(specs) != 2 {
		t.Fatalf("ModelSpecs: len = %d, want 2", len(specs))
	}
	if specs[opus] != opusSpec {
		t.Errorf("ModelSpecs[opus] = %p, want %p", specs[opus], opusSpec)
	}
	if specs[haiku] != haikuSpec {
		t.Errorf("ModelSpecs[haiku] = %p, want %p", specs[haiku], haikuSpec)
	}

	// Freshly built each call: mutating the result must not disturb the
	// catalog, since consumers pass this map straight into SelectModel.
	delete(specs, opus)
	if again := cat.ModelSpecs(); len(again) != 2 {
		t.Errorf("ModelSpecs: caller mutation leaked into the catalog: len = %d, want 2", len(again))
	}
}

func TestCatalogToolNames(t *testing.T) {
	t.Parallel()

	cat := fake.New().
		AddTool("fs", "write_file", providercatalog.ToolHandle{}).
		AddTool("fs", "read_file", providercatalog.ToolHandle{}).
		AddTool("fs", "list_dir", providercatalog.ToolHandle{}).
		AddTool("shell", "run", providercatalog.ToolHandle{})

	got := cat.ToolNames()
	want := map[string][]string{
		"fs":    {"list_dir", "read_file", "write_file"},
		"shell": {"run"},
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("ToolNames = %v, want %v", got, want)
	}

	// Sorting is what makes the result assertable at all: repeated calls
	// must not vary with map iteration order.
	for range 5 {
		if again := cat.ToolNames(); !maps.EqualFunc(again, want, slices.Equal) {
			t.Fatalf("ToolNames: unstable across calls: %v, want %v", again, want)
		}
	}

	if names := fake.New().ToolNames(); len(names) != 0 {
		t.Errorf("ToolNames on an empty catalog = %v, want empty", names)
	}
}

func TestZeroValueCatalog(t *testing.T) {
	t.Parallel()

	var cat fake.Catalog

	if _, err := cat.Model(agentprofile.ModelRef{Provider: "anthropic", ID: "x"}); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Errorf("Model on zero Catalog: want ErrNotFound, got %v", err)
	}
	if _, err := cat.Tool("fs", "read_file"); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Errorf("Tool on zero Catalog: want ErrNotFound, got %v", err)
	}
	if _, err := cat.Hook("fs"); !errors.Is(err, providercatalog.ErrNotFound) {
		t.Errorf("Hook on zero Catalog: want ErrNotFound, got %v", err)
	}
	if specs := cat.ModelSpecs(); len(specs) != 0 {
		t.Errorf("ModelSpecs on zero Catalog = %v, want empty", specs)
	}
	if names := cat.ToolNames(); len(names) != 0 {
		t.Errorf("ToolNames on zero Catalog = %v, want empty", names)
	}
	if contexts := cat.Contexts(); len(contexts) != 0 {
		t.Errorf("Contexts on zero Catalog = %v, want empty", contexts)
	}

	// The Add methods must work on a zero Catalog too, so a test can
	// start from a struct literal and keep building.
	cat.AddModel(agentprofile.ModelRef{Provider: "anthropic", ID: "x"}, providercatalog.ModelHandle{})
	cat.AddTool("fs", "read_file", providercatalog.ToolHandle{})
	cat.AddHook("fs", providercatalog.HookHandle{})
	if _, err := cat.Model(agentprofile.ModelRef{Provider: "anthropic", ID: "x"}); err != nil {
		t.Errorf("Model after AddModel on zero Catalog: %v", err)
	}
	if _, err := cat.Tool("fs", "read_file"); err != nil {
		t.Errorf("Tool after AddTool on zero Catalog: %v", err)
	}
	if _, err := cat.Hook("fs"); err != nil {
		t.Errorf("Hook after AddHook on zero Catalog: %v", err)
	}
}

func TestAddReplacesExistingHandle(t *testing.T) {
	t.Parallel()

	ref := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"}
	second := &modelv1.ModelSpec{Id: ref.ID, ContextWindow: 1_000_000}

	cat := fake.New().
		AddModel(ref, providercatalog.ModelHandle{Spec: &modelv1.ModelSpec{Id: ref.ID, ContextWindow: 200_000}}).
		AddModel(ref, providercatalog.ModelHandle{Spec: second})

	got, err := cat.Model(ref)
	if err != nil {
		t.Fatalf("Model: unexpected error: %v", err)
	}
	if got.Spec != second {
		t.Errorf("Model: Spec = %p, want the second registration %p", got.Spec, second)
	}
}

// TestComposesWithAgentprofile is the load-bearing shape check: it feeds
// ModelSpecs and ToolNames straight into the real
// agentprofile.SelectModel / agentprofile.ResolveTools, so the map types
// are proven to compose rather than merely to look alike.
func TestComposesWithAgentprofile(t *testing.T) {
	t.Parallel()

	opus := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"}
	haiku := agentprofile.ModelRef{Provider: "anthropic", ID: "claude-haiku-4"}

	cat := fake.New().
		// Primary: no tool use, so a tool-using turn must skip it.
		AddModel(opus, providercatalog.ModelHandle{Spec: &modelv1.ModelSpec{
			Id:            opus.ID,
			ContextWindow: 200_000,
		}}).
		AddModel(haiku, providercatalog.ModelHandle{Spec: &modelv1.ModelSpec{
			Id:              haiku.ID,
			ContextWindow:   200_000,
			SupportsToolUse: true,
		}}).
		AddTool("fs", "read_file", providercatalog.ToolHandle{}).
		AddTool("fs", "write_file", providercatalog.ToolHandle{}).
		AddTool("shell", "run", providercatalog.ToolHandle{})

	block := agentprofile.ModelBlock{Primary: opus, Fallbacks: []agentprofile.ModelRef{haiku}}

	t.Run("SelectModel routes on catalog specs", func(t *testing.T) {
		t.Parallel()

		got, err := agentprofile.SelectModel(block, cat.ModelSpecs(), agentprofile.TurnRequirements{NeedsToolUse: true})
		if err != nil {
			t.Fatalf("SelectModel: unexpected error: %v", err)
		}
		if got != haiku {
			t.Errorf("SelectModel = %+v, want the tool-using fallback %+v", got, haiku)
		}

		plain, err := agentprofile.SelectModel(block, cat.ModelSpecs(), agentprofile.TurnRequirements{})
		if err != nil {
			t.Fatalf("SelectModel (no requirements): unexpected error: %v", err)
		}
		if plain != opus {
			t.Errorf("SelectModel (no requirements) = %+v, want the primary %+v", plain, opus)
		}

		// A ref the catalog does not carry is skipped, not an error —
		// until the whole chain is exhausted.
		absent := agentprofile.ModelBlock{Primary: agentprofile.ModelRef{Provider: "openai", ID: "gpt-5"}}
		if _, err := agentprofile.SelectModel(absent, cat.ModelSpecs(), agentprofile.TurnRequirements{}); !errors.Is(err, agentprofile.ErrNoEligibleModel) {
			t.Errorf("SelectModel (absent chain): want ErrNoEligibleModel, got %v", err)
		}
	})

	t.Run("ResolveTools expands against catalog names", func(t *testing.T) {
		t.Parallel()

		resolved, err := agentprofile.ResolveTools([]string{"fs.*", "shell.run"}, cat.ToolNames())
		if err != nil {
			t.Fatalf("ResolveTools: unexpected error: %v", err)
		}
		want := []string{"fs.read_file", "fs.write_file", "shell.run"}
		got := slices.Sorted(maps.Keys(resolved))
		if !slices.Equal(got, want) {
			t.Fatalf("ResolveTools = %v, want %v", got, want)
		}

		// A typo'd tool on a provider the catalog does carry is an
		// error, which only works if ToolNames really is the map
		// ResolveTools validates against.
		if _, err := agentprofile.ResolveTools([]string{"fs.reed_file"}, cat.ToolNames()); !errors.Is(err, agentprofile.ErrUnknownTool) {
			t.Errorf("ResolveTools (typo): want ErrUnknownTool, got %v", err)
		}
	})

	t.Run("resolved tools reach live handles", func(t *testing.T) {
		t.Parallel()

		resolved, err := agentprofile.ResolveTools([]string{"fs.*"}, cat.ToolNames())
		if err != nil {
			t.Fatalf("ResolveTools: unexpected error: %v", err)
		}
		for key := range resolved {
			provider, tool, _ := strings.Cut(key, ".")
			if _, err := cat.Tool(provider, tool); err != nil {
				t.Errorf("Tool(%q, %q) for resolved key %q: %v", provider, tool, key, err)
			}
		}
	})
}
