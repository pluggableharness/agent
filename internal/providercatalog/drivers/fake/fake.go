package fake

import (
	"cmp"
	"fmt"
	"slices"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/providercatalog"
)

// ToolKey is the composite lookup key for a tool operation: the
// provider's agent.hcl local name plus the operation name, i.e. the two
// halves of a "<provider>.<tool>" scoping entry.
type ToolKey struct {
	// Provider is the tool provider's agent.hcl local name.
	Provider string
	// Tool is the operation name that provider advertises.
	Tool string
}

// Catalog is a scripted, in-memory providercatalog.Catalog for tests.
// Build one with New plus the Add methods, or as a struct literal when
// a scenario needs state the adders cannot express (see the package
// doc). The zero value is usable and reports ErrNotFound for every
// lookup.
//
// The Add methods mutate the Catalog, so they are construction-time
// only: finish building before handing a Catalog to the code under
// test. Once built, concurrent lookups are safe — nothing here writes
// during a read.
type Catalog struct {
	// Models is every loaded model, keyed by ref.
	Models map[agentprofile.ModelRef]providercatalog.ModelHandle
	// Tools is every loaded tool operation, keyed by provider local
	// name plus operation name.
	Tools map[ToolKey]providercatalog.ToolHandle
	// ContextProviders is every loaded context provider. Contexts
	// returns them ordered by Position, so this slice's own order does
	// not matter.
	ContextProviders []providercatalog.ContextHandle
	// Hooks is every loaded hook subscriber, keyed by the plugin's
	// agent.hcl local name.
	Hooks map[string]providercatalog.HookHandle
}

// New returns an empty Catalog with initialized maps, ready for the Add
// methods.
func New() *Catalog {
	return &Catalog{
		Models: make(map[agentprofile.ModelRef]providercatalog.ModelHandle),
		Tools:  make(map[ToolKey]providercatalog.ToolHandle),
		Hooks:  make(map[string]providercatalog.HookHandle),
	}
}

// AddModel registers h under ref, stamping ref into h.Ref so a scenario
// names the model once. It returns c for chaining, and replaces any
// handle already registered under ref.
func (c *Catalog) AddModel(ref agentprofile.ModelRef, h providercatalog.ModelHandle) *Catalog {
	if c.Models == nil {
		c.Models = make(map[agentprofile.ModelRef]providercatalog.ModelHandle)
	}
	h.Ref = ref
	c.Models[ref] = h
	return c
}

// AddTool registers h under (provider, tool), stamping provider into
// h.Provider. The operation name is a parameter rather than read from
// h.Schema.Name so a scenario that does not care about schemas can pass
// a zero ToolHandle. It returns c for chaining, and replaces any handle
// already registered under the same pair.
func (c *Catalog) AddTool(provider, tool string, h providercatalog.ToolHandle) *Catalog {
	if c.Tools == nil {
		c.Tools = make(map[ToolKey]providercatalog.ToolHandle)
	}
	h.Provider = provider
	c.Tools[ToolKey{Provider: provider, Tool: tool}] = h
	return c
}

// AddContext appends h as the next declared context provider, stamping
// h.Position with its append index — call order is declaration order.
// A scenario needing non-sequential positions (a gap, a deliberate
// out-of-order slice) assigns ContextProviders directly instead. It
// returns c for chaining.
func (c *Catalog) AddContext(h providercatalog.ContextHandle) *Catalog {
	h.Position = len(c.ContextProviders)
	c.ContextProviders = append(c.ContextProviders, h)
	return c
}

// AddHook registers h under provider, the plugin's agent.hcl local
// name. The name is a parameter because HookHandle carries no local
// name of its own — a hook subscription rides the connection of
// whichever category service the plugin primarily serves. It returns c
// for chaining, and replaces any handle already registered under
// provider.
func (c *Catalog) AddHook(provider string, h providercatalog.HookHandle) *Catalog {
	if c.Hooks == nil {
		c.Hooks = make(map[string]providercatalog.HookHandle)
	}
	c.Hooks[provider] = h
	return c
}

// Model returns the handle registered for ref, or ErrNotFound.
func (c *Catalog) Model(ref agentprofile.ModelRef) (providercatalog.ModelHandle, error) {
	h, ok := c.Models[ref]
	if !ok {
		return providercatalog.ModelHandle{}, fmt.Errorf("providercatalog/fake: model %q.%q: %w", ref.Provider, ref.ID, providercatalog.ErrNotFound)
	}
	return h, nil
}

// ModelSpecs returns every registered model's Spec keyed by ref, in the
// shape agentprofile.SelectModel consumes. The returned map is freshly
// built, so a caller may retain or mutate it without disturbing c.
func (c *Catalog) ModelSpecs() map[agentprofile.ModelRef]*modelv1.ModelSpec {
	specs := make(map[agentprofile.ModelRef]*modelv1.ModelSpec, len(c.Models))
	for ref, h := range c.Models {
		specs[ref] = h.Spec
	}
	return specs
}

// Tool returns the handle registered for (provider, tool), or
// ErrNotFound.
func (c *Catalog) Tool(provider, tool string) (providercatalog.ToolHandle, error) {
	h, ok := c.Tools[ToolKey{Provider: provider, Tool: tool}]
	if !ok {
		return providercatalog.ToolHandle{}, fmt.Errorf("providercatalog/fake: tool %q.%q: %w", provider, tool, providercatalog.ErrNotFound)
	}
	return h, nil
}

// ToolNames returns each registered provider's operation names, in the
// shape agentprofile.ResolveTools consumes. Names are sorted so a test
// asserting on the result never depends on map iteration order
// (.claude/rules/determinism.md).
func (c *Catalog) ToolNames() map[string][]string {
	names := make(map[string][]string)
	for key := range c.Tools {
		names[key.Provider] = append(names[key.Provider], key.Tool)
	}
	for provider := range names {
		slices.Sort(names[provider])
	}
	return names
}

// Contexts returns every registered context provider ordered by
// Position, honoring the interface's declaration-order contract even
// when ContextProviders was assigned out of order by a struct literal.
// The returned slice is a fresh copy.
func (c *Catalog) Contexts() []providercatalog.ContextHandle {
	out := slices.Clone(c.ContextProviders)
	slices.SortStableFunc(out, func(a, b providercatalog.ContextHandle) int {
		return cmp.Compare(a.Position, b.Position)
	})
	return out
}

// Hook returns the handle registered for provider, or ErrNotFound.
func (c *Catalog) Hook(provider string) (providercatalog.HookHandle, error) {
	h, ok := c.Hooks[provider]
	if !ok {
		return providercatalog.HookHandle{}, fmt.Errorf("providercatalog/fake: hook %q: %w", provider, providercatalog.ErrNotFound)
	}
	return h, nil
}

var _ providercatalog.Catalog = (*Catalog)(nil)
