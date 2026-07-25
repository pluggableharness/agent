package pluginhost_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/pluggableharness/agent/internal/pluginhost"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// live builds a *pluginhost.Live for registry tests. The client is a nil
// typed client — these tests exercise lookup and ordering, which never
// dial anything.
func live(localName string, category commonv1.Category, name string, index int, client any) *pluginhost.Live {
	return &pluginhost.Live{
		LocalName:   localName,
		Producer:    &commonv1.ProducerRef{Category: category, Name: name, Version: "1.0.0"},
		Client:      client,
		LaunchIndex: index,
	}
}

// nilModelClient returns a typed-but-nil ModelServiceClient, so a type
// assertion against the interface succeeds without a connection.
func nilModelClient() modelv1.ModelServiceClient {
	return modelv1.NewModelServiceClient(nil)
}

func nilToolClient() toolv1.ToolServiceClient {
	return toolv1.NewToolServiceClient(nil)
}

func nilContextClient() contextv1.ContextServiceClient {
	return contextv1.NewContextServiceClient(nil)
}

func TestRegistry_addAndLookup(t *testing.T) {
	t.Parallel()

	r := pluginhost.NewRegistry()
	anthropic := live("anthropic", commonv1.Category_CATEGORY_MODEL, "claude", 0, nilModelClient())
	fs := live("filesystem", commonv1.Category_CATEGORY_TOOL, "fs", 1, nilToolClient())

	for _, l := range []*pluginhost.Live{anthropic, fs} {
		if err := r.Add(l); err != nil {
			t.Fatalf("Add(%s): %v", l.LocalName, err)
		}
	}

	if got, ok := r.ByKey(pluginhost.Key{Category: commonv1.Category_CATEGORY_MODEL, Name: "claude"}); !ok || got != anthropic {
		t.Errorf("ByKey(model/claude) = %v, %v; want the anthropic entry", got, ok)
	}
	if got, ok := r.ByLocalName("filesystem"); !ok || got != fs {
		t.Errorf("ByLocalName(filesystem) = %v, %v; want the filesystem entry", got, ok)
	}

	// The two names are not interchangeable: the local name is the
	// operator's label, the key carries the plugin's published name.
	if _, ok := r.ByKey(pluginhost.Key{Category: commonv1.Category_CATEGORY_MODEL, Name: "anthropic"}); ok {
		t.Error("ByKey resolved the agent.hcl local name; it must key on the plugin's published name")
	}
	if _, ok := r.ByLocalName("claude"); ok {
		t.Error("ByLocalName resolved the plugin's published name; it must key on the agent.hcl local name")
	}
	if _, ok := r.ByLocalName("absent"); ok {
		t.Error("ByLocalName(absent) reported ok, want false")
	}
}

func TestRegistry_duplicateKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		first  *pluginhost.Live
		second *pluginhost.Live
	}{
		{
			name:   "same category and published name under two local names",
			first:  live("a", commonv1.Category_CATEGORY_MODEL, "claude", 0, nilModelClient()),
			second: live("b", commonv1.Category_CATEGORY_MODEL, "claude", 1, nilModelClient()),
		},
		{
			name:   "same local name twice",
			first:  live("a", commonv1.Category_CATEGORY_MODEL, "one", 0, nilModelClient()),
			second: live("a", commonv1.Category_CATEGORY_TOOL, "two", 1, nilToolClient()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := pluginhost.NewRegistry()
			if err := r.Add(tt.first); err != nil {
				t.Fatalf("Add(first): %v", err)
			}
			err := r.Add(tt.second)
			if !errors.Is(err, pluginhost.ErrDuplicateKey) {
				t.Fatalf("Add(second) = %v, want ErrDuplicateKey", err)
			}
			if len(r.All()) != 1 {
				t.Errorf("All() = %d entries after a rejected Add, want 1 — a rejected Add must not append", len(r.All()))
			}
		})
	}
}

// TestRegistry_sameNameDifferentCategories confirms Key's category half
// is real: "anthropic" as a model and "anthropic" as a memory backend are
// different producers (commonv1.ProducerRef's own doc comment).
func TestRegistry_sameNameDifferentCategories(t *testing.T) {
	t.Parallel()

	r := pluginhost.NewRegistry()
	if err := r.Add(live("a", commonv1.Category_CATEGORY_MODEL, "anthropic", 0, nilModelClient())); err != nil {
		t.Fatalf("Add(model): %v", err)
	}
	if err := r.Add(live("b", commonv1.Category_CATEGORY_MEMORY, "anthropic", 1, nil)); err != nil {
		t.Fatalf("Add(memory): %v — same name in a different category is a different producer", err)
	}
	if len(r.All()) != 2 {
		t.Errorf("All() = %d entries, want 2", len(r.All()))
	}
}

func TestRegistry_orderingAndFiltering(t *testing.T) {
	t.Parallel()

	r := pluginhost.NewRegistry()
	want := []string{"first", "second", "third", "fourth"}
	entries := []*pluginhost.Live{
		live("first", commonv1.Category_CATEGORY_CONTEXT, "ctx-a", 0, nilContextClient()),
		live("second", commonv1.Category_CATEGORY_MODEL, "claude", 1, nilModelClient()),
		live("third", commonv1.Category_CATEGORY_CONTEXT, "ctx-b", 2, nilContextClient()),
		live("fourth", commonv1.Category_CATEGORY_TOOL, "fs", 3, nilToolClient()),
	}
	for _, l := range entries {
		if err := r.Add(l); err != nil {
			t.Fatalf("Add(%s): %v", l.LocalName, err)
		}
	}

	got := make([]string, 0, len(want))
	for _, l := range r.All() {
		got = append(got, l.LocalName)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("All() = %v, want launch order %v", got, want)
	}

	contexts := r.ByCategory(commonv1.Category_CATEGORY_CONTEXT)
	if len(contexts) != 2 || contexts[0].LocalName != "first" || contexts[1].LocalName != "third" {
		t.Errorf("ByCategory(context) = %v, want [first third] in launch order", contexts)
	}
	if got := r.ByCategory(commonv1.Category_CATEGORY_WIDGET); len(got) != 0 || got == nil {
		t.Errorf("ByCategory(widget) = %v, want an empty non-nil slice", got)
	}
}

// TestRegistry_allReturnsACopy guards the promise that a caller cannot
// reorder the registry by sorting what All handed it.
func TestRegistry_allReturnsACopy(t *testing.T) {
	t.Parallel()

	r := pluginhost.NewRegistry()
	for i, name := range []string{"a", "b"} {
		if err := r.Add(live(name, commonv1.Category_CATEGORY_TOOL, name, i, nilToolClient())); err != nil {
			t.Fatalf("Add(%s): %v", name, err)
		}
	}

	got := r.All()
	got[0], got[1] = got[1], got[0]

	if again := r.All(); again[0].LocalName != "a" {
		t.Errorf("All()[0] = %q after the caller reordered a previous result, want %q", again[0].LocalName, "a")
	}
}

// TestRegistry_concurrentReads exercises the RWMutex under -race: many
// concurrent lookups against a registry being written to.
func TestRegistry_concurrentReads(t *testing.T) {
	t.Parallel()

	r := pluginhost.NewRegistry()
	var wg sync.WaitGroup

	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := string(rune('a' + i))
			if err := r.Add(live(name, commonv1.Category_CATEGORY_TOOL, name, i, nilToolClient())); err != nil {
				t.Errorf("Add(%s): %v", name, err)
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ByLocalName("a")
			r.ByKey(pluginhost.Key{Category: commonv1.Category_CATEGORY_TOOL, Name: "b"})
			r.ByCategory(commonv1.Category_CATEGORY_TOOL)
			r.All()
		}()
	}
	wg.Wait()

	if len(r.All()) != 8 {
		t.Errorf("All() = %d entries, want 8", len(r.All()))
	}
}

func TestLive_categoryAccessors(t *testing.T) {
	t.Parallel()

	model := live("m", commonv1.Category_CATEGORY_MODEL, "claude", 0, nilModelClient())
	tool := live("t", commonv1.Category_CATEGORY_TOOL, "fs", 1, nilToolClient())
	contextProvider := live("c", commonv1.Category_CATEGORY_CONTEXT, "claude-md", 2, nilContextClient())

	if _, ok := model.ModelClient(); !ok {
		t.Error("ModelClient() on a model plugin reported ok = false")
	}
	if _, ok := model.ToolClient(); ok {
		t.Error("ToolClient() on a model plugin reported ok = true")
	}
	if _, ok := tool.ToolClient(); !ok {
		t.Error("ToolClient() on a tool plugin reported ok = false")
	}
	if _, ok := contextProvider.ContextClient(); !ok {
		t.Error("ContextClient() on a context plugin reported ok = false")
	}
	if _, ok := contextProvider.ModelClient(); ok {
		t.Error("ModelClient() on a context plugin reported ok = true")
	}

	// Never launched, so there is no shared connection to dial hooks on.
	if _, ok := model.HookClient(); ok {
		t.Error("HookClient() on a Live that never came from a launch reported ok = true")
	}

	for _, tc := range []struct {
		name string
		got  bool
	}{
		{"memory", func() bool { _, ok := model.MemoryClient(); return ok }()},
		{"frontend", func() bool { _, ok := model.FrontendClient(); return ok }()},
		{"widget", func() bool { _, ok := model.WidgetClient(); return ok }()},
		{"slashcommand", func() bool { _, ok := model.SlashCommandClient(); return ok }()},
	} {
		if tc.got {
			t.Errorf("%sClient() on a model plugin reported ok = true", tc.name)
		}
	}
}

// TestRegistry_satisfiesModelLookup is the compile-and-behavior proof
// that Registry structurally satisfies internal/tokencount.ModelLookup
// without either package importing the other. The interface is restated
// locally rather than imported, exactly as a consumer would declare it.
func TestRegistry_satisfiesModelLookup(t *testing.T) {
	t.Parallel()

	type modelLookup interface {
		ModelClientByLocalName(name string) (modelv1.ModelServiceClient, bool)
	}

	r := pluginhost.NewRegistry()
	if err := r.Add(live("anthropic", commonv1.Category_CATEGORY_MODEL, "claude", 0, nilModelClient())); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Add(live("filesystem", commonv1.Category_CATEGORY_TOOL, "fs", 1, nilToolClient())); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var lookup modelLookup = r

	if _, ok := lookup.ModelClientByLocalName("anthropic"); !ok {
		t.Error("ModelClientByLocalName(anthropic) reported ok = false, want true")
	}
	if _, ok := lookup.ModelClientByLocalName("filesystem"); ok {
		t.Error("ModelClientByLocalName(filesystem) reported ok = true for a tool plugin, want false")
	}
	if _, ok := lookup.ModelClientByLocalName("absent"); ok {
		t.Error("ModelClientByLocalName(absent) reported ok = true, want false")
	}
}
