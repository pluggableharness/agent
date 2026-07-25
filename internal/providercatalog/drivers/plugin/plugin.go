package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/pluginhost"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// Config bundles Catalog's build-time dependencies. Registry and
// Telemetry MUST be set — New panics if either is nil, since a Catalog
// with no registry has nothing to extract from and a nil Telemetry
// would otherwise panic obscurely deep inside the OTel SDK on the first
// Preview probe. Logger defaults to slog.Default() when nil.
type Config struct {
	// Registry is the already-populated registry New extracts from.
	// MUST be set, and MUST NOT be mutated concurrently with New's own
	// read pass — see doc.go's "Eager vs. lazy extraction" for why that
	// is already guaranteed by pluginhost.Supervisor's own lifecycle.
	Registry *pluginhost.Registry
	// Telemetry provides the span this package's one non-trivial
	// operation — the Preview support probes New performs — is wrapped
	// in, per .claude/rules/logging-telemetry.md. MUST be set.
	Telemetry *telemetry.Provider
	// Logger receives DEBUG/WARN diagnostics for the extraction pass and
	// each Preview probe. Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// Catalog implements providercatalog.Catalog over a live
// *pluginhost.Registry. Construct with New; the zero value is not
// usable (its maps and slice are all nil, so every lookup reports
// providercatalog.ErrNotFound, but that is an implementation detail, not
// a documented usable state — always go through New).
type Catalog struct {
	models   map[agentprofile.ModelRef]providercatalog.ModelHandle
	tools    map[toolKey]providercatalog.ToolHandle
	contexts []providercatalog.ContextHandle // pre-sorted by Position
	hooks    map[string]providercatalog.HookHandle
}

var _ providercatalog.Catalog = (*Catalog)(nil)

// New builds a Catalog over every plugin currently registered in
// cfg.Registry, extracting every model spec, tool schema, context
// capability, and hook subscription up front — see doc.go's "Eager vs.
// lazy extraction". ctx bounds the one-time Preview probes New performs
// to resolve each ToolHandle.SupportsPreview (doc.go's "Resolving
// SupportsPreview"); it is not retained past New's return.
func New(ctx context.Context, cfg Config) *Catalog {
	if cfg.Registry == nil {
		panic("providercatalog/plugin: New: cfg.Registry is nil")
	}
	if cfg.Telemetry == nil {
		panic("providercatalog/plugin: New: cfg.Telemetry is nil")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	ctx, span := cfg.Telemetry.StartProviderCatalogBuild(ctx)
	defer func() { telemetry.EndSpan(span, nil) }()

	logger.DebugContext(ctx, "providercatalog/plugin: building catalog")

	c := &Catalog{
		models:   buildModels(ctx, logger, cfg.Registry),
		tools:    buildTools(ctx, cfg.Telemetry, logger, cfg.Registry),
		contexts: buildContexts(ctx, logger, cfg.Registry),
		hooks:    buildHooks(cfg.Registry),
	}

	logger.InfoContext(ctx, "providercatalog/plugin: catalog built",
		"models", len(c.models), "tools", len(c.tools),
		"contexts", len(c.contexts), "hooks", len(c.hooks))
	return c
}

// Model resolves ref to a live handle, or providercatalog.ErrNotFound if
// that provider or model id is not loaded.
func (c *Catalog) Model(ref agentprofile.ModelRef) (providercatalog.ModelHandle, error) {
	h, ok := c.models[ref]
	if !ok {
		return providercatalog.ModelHandle{}, fmt.Errorf("providercatalog/plugin: model %q.%q: %w", ref.Provider, ref.ID, providercatalog.ErrNotFound)
	}
	return h, nil
}

// ModelSpecs returns every currently-loaded model's declared spec, keyed
// by ref. The returned map is freshly built on every call, so a caller
// may retain or mutate it without disturbing c.
func (c *Catalog) ModelSpecs() map[agentprofile.ModelRef]*modelv1.ModelSpec {
	specs := make(map[agentprofile.ModelRef]*modelv1.ModelSpec, len(c.models))
	for ref, h := range c.models {
		specs[ref] = h.Spec
	}
	return specs
}

// Tool resolves a provider local name and operation name to a live
// handle, or providercatalog.ErrNotFound if that provider is not loaded
// or does not advertise that operation.
func (c *Catalog) Tool(provider, tool string) (providercatalog.ToolHandle, error) {
	h, ok := c.tools[toolKey{provider: provider, tool: tool}]
	if !ok {
		return providercatalog.ToolHandle{}, fmt.Errorf("providercatalog/plugin: tool %q.%q: %w", provider, tool, providercatalog.ErrNotFound)
	}
	return h, nil
}

// ToolNames returns every loaded tool provider's advertised operation
// names, keyed by local name. Names are sorted per-provider so a caller
// asserting on the result never depends on map iteration order
// (.claude/rules/determinism.md).
func (c *Catalog) ToolNames() map[string][]string {
	names := make(map[string][]string)
	for key := range c.tools {
		names[key.provider] = append(names[key.provider], key.tool)
	}
	for provider := range names {
		slices.Sort(names[provider])
	}
	return names
}

// Contexts returns every loaded context provider's handle, ordered by
// Position. The returned slice is a fresh copy, so a caller cannot
// reorder c's internal state by sorting or mutating what it gets back.
func (c *Catalog) Contexts() []providercatalog.ContextHandle {
	return slices.Clone(c.contexts)
}

// Hook resolves a loaded plugin's HookSubscriberService by its agent.hcl
// local name, or providercatalog.ErrNotFound if that plugin is not
// loaded or serves no hooks.
func (c *Catalog) Hook(provider string) (providercatalog.HookHandle, error) {
	h, ok := c.hooks[provider]
	if !ok {
		return providercatalog.HookHandle{}, fmt.Errorf("providercatalog/plugin: hook %q: %w", provider, providercatalog.ErrNotFound)
	}
	return h, nil
}
