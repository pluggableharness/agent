package plugin

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"sync"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/pluginhost"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// toolKey is the composite lookup key for one tool operation: the
// provider's agent.hcl local name plus the operation name — the two
// halves of a "<provider>.<tool>" scoping entry. Mirrors
// drivers/fake.ToolKey; kept unexported here since every Catalog field
// is unexported too and no test needs to build one directly (unlike
// the fake, whose struct-literal construction path is a documented,
// intentional escape hatch).
type toolKey struct {
	provider string
	tool     string
}

// buildModels extracts every model spec of every loaded model-category
// plugin in reg, keyed by {provider local name, model id}.
func buildModels(ctx context.Context, logger *slog.Logger, reg *pluginhost.Registry) map[agentprofile.ModelRef]providercatalog.ModelHandle {
	lives := reg.ByCategory(commonv1.Category_CATEGORY_MODEL)
	models := make(map[agentprofile.ModelRef]providercatalog.ModelHandle)
	for _, live := range lives {
		resp, ok := live.Capabilities.(*modelv1.GetCapabilitiesResponse)
		if !ok || resp.GetCapabilities() == nil {
			logger.WarnContext(ctx, "providercatalog/plugin: model provider capabilities missing or wrong type",
				"provider", live.LocalName)
			continue
		}
		client, _ := live.ModelClient()
		for _, spec := range resp.GetCapabilities().GetModels() {
			ref := agentprofile.ModelRef{Provider: live.LocalName, ID: spec.GetId()}
			models[ref] = providercatalog.ModelHandle{
				Ref:      ref,
				Producer: live.Producer,
				Spec:     spec,
				Client:   client,
			}
		}
	}
	return models
}

// buildTools extracts every tool operation of every loaded tool-category
// plugin in reg, keyed by {provider local name, operation name},
// resolving SupportsPreview for each operation concurrently — see
// doc.go's "Resolving SupportsPreview".
func buildTools(ctx context.Context, tel *telemetry.Provider, logger *slog.Logger, reg *pluginhost.Registry) map[toolKey]providercatalog.ToolHandle {
	lives := reg.ByCategory(commonv1.Category_CATEGORY_TOOL)
	tools := make(map[toolKey]providercatalog.ToolHandle)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, live := range lives {
		resp, ok := live.Capabilities.(*toolv1.GetSchemaResponse)
		if !ok {
			logger.WarnContext(ctx, "providercatalog/plugin: tool provider capabilities missing or wrong type",
				"provider", live.LocalName)
			continue
		}
		client, _ := live.ToolClient()
		producer := live.Producer
		localName := live.LocalName
		for _, schema := range dedupeSchemas(ctx, logger, localName, resp.GetTools()) {
			wg.Add(1)
			go func(schema *toolv1.ToolSchema) {
				defer wg.Done()
				supports := resolveSupportsPreview(ctx, tel, logger, client, producer, schema.GetName())
				h := providercatalog.ToolHandle{
					Provider:        localName,
					Producer:        producer,
					Schema:          schema,
					Client:          client,
					SupportsPreview: supports,
					TerminatesTurn:  schema.GetTerminatesTurn(),
				}
				mu.Lock()
				tools[toolKey{provider: localName, tool: schema.GetName()}] = h
				mu.Unlock()
			}(schema)
		}
	}
	wg.Wait()
	return tools
}

// dedupeSchemas returns schemas with any repeated operation name removed,
// keeping the FIRST occurrence in the provider's own advertised order.
//
// Without this, two schemas sharing a name race for the same toolKey from
// two goroutines. The map write is mutex-guarded so there is no data race,
// but WHICH of the two lands is decided by goroutine completion order —
// not deterministic, and not reproducible run to run.
//
// That matters well beyond tidiness. A ToolSchema carries the kind and the
// risk class the plan/apply gate classifies a call by (internal/policy's
// Evaluate matches on both), so a provider advertising one operation name
// twice with differing kind/risk would get a policy decision that varied
// between runs of the same session — a data_source read on one run and a
// gated resource mutation on the next. Resolving the collision here,
// before the fan-out, makes the catalog a pure function of the provider's
// advertised order regardless of scheduling.
//
// First-wins rather than an error because this driver's extraction path
// has no error channel to return one through; a malformed advertisement
// degrades to a loud WARN naming the provider and the operation, matching
// how every other unusable capability response in this file is handled.
// internal/pluginhost's Registry.Add applies the same "reject rather than
// silently overwrite" posture one level up, for two plugins claiming one
// {category, name}.
func dedupeSchemas(ctx context.Context, logger *slog.Logger, provider string, schemas []*toolv1.ToolSchema) []*toolv1.ToolSchema {
	seen := make(map[string]struct{}, len(schemas))
	out := make([]*toolv1.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		name := schema.GetName()
		if _, dup := seen[name]; dup {
			logger.WarnContext(ctx, "providercatalog/plugin: tool provider advertised a duplicate operation name; keeping the first occurrence and ignoring the rest",
				"provider", provider, "tool", name)
			continue
		}
		seen[name] = struct{}{}
		out = append(out, schema)
	}
	return out
}

// buildContexts extracts every loaded context-category plugin in reg,
// ordered by Live.LaunchIndex — agent.hcl declaration order, confirmed
// in doc.go's "Position and LaunchIndex" section. TokenBudget is always
// Capabilities.DefaultTokenBudget; see doc.go's "A known gap" section
// for why an agent.hcl token_budget override cannot be resolved here.
func buildContexts(ctx context.Context, logger *slog.Logger, reg *pluginhost.Registry) []providercatalog.ContextHandle {
	lives := reg.ByCategory(commonv1.Category_CATEGORY_CONTEXT)
	out := make([]providercatalog.ContextHandle, 0, len(lives))
	for _, live := range lives {
		resp, ok := live.Capabilities.(*contextv1.GetCapabilitiesResponse)
		if !ok || resp.GetCapabilities() == nil {
			logger.WarnContext(ctx, "providercatalog/plugin: context provider capabilities missing or wrong type",
				"provider", live.LocalName)
			continue
		}
		client, _ := live.ContextClient()
		caps := resp.GetCapabilities()
		out = append(out, providercatalog.ContextHandle{
			Provider:     live.LocalName,
			Producer:     live.Producer,
			Capabilities: caps,
			Client:       client,
			Position:     live.LaunchIndex,
			TokenBudget:  caps.GetDefaultTokenBudget(),
		})
	}
	// ByCategory already returns launch order, so this is defensive —
	// it guards the interface's own ordering promise against any future
	// change to how out is assembled, matching drivers/fake.Contexts's
	// same defensive sort.
	slices.SortStableFunc(out, func(a, b providercatalog.ContextHandle) int {
		return cmp.Compare(a.Position, b.Position)
	})
	return out
}

// buildHooks extracts every loaded plugin's HookSubscriberService, keyed
// by agent.hcl local name, for every plugin whose HookClient is
// reachable and whose category capabilities advertise at least one
// supported hook point. A plugin failing either test — no HookClient
// (pluginhost.Live.HookClient's own ok=false case), or an empty
// SupportedHookPoints — "serves no hooks" per
// providercatalog.Catalog.Hook's own doc comment, and is simply absent
// from the returned map rather than present with an unusable handle.
func buildHooks(reg *pluginhost.Registry) map[string]providercatalog.HookHandle {
	lives := reg.All()
	hooks := make(map[string]providercatalog.HookHandle, len(lives))
	for _, live := range lives {
		client, ok := live.HookClient()
		if !ok {
			continue
		}
		points := supportedHookPoints(live.Producer.GetCategory(), live.Capabilities)
		if len(points) == 0 {
			continue
		}
		hooks[live.LocalName] = providercatalog.HookHandle{
			Producer:        live.Producer,
			Client:          client,
			SupportedPoints: points,
		}
	}
	return hooks
}

// supportedHookPoints extracts SupportedHookPoints from capabilities,
// dispatching on category since every one of the seven plugin
// categories carries the field at a different nesting depth — some
// nested under a per-category Capabilities message (model, context,
// memory, frontend, widget), some flat on the response itself (tool,
// slashcommand). Confirmed by reading every pkg/<category>/proto/v1
// package directly: all seven carry SupportedHookPoints somewhere: none
// of the seven capability responses lacks it. Returns nil for an
// unrecognized category or a capabilities value of the wrong Go type —
// the same defensive miss every other extractor in this file logs and
// skips on.
func supportedHookPoints(category commonv1.Category, capabilities any) []commonv1.HookPoint {
	switch category {
	case commonv1.Category_CATEGORY_MODEL:
		if resp, ok := capabilities.(*modelv1.GetCapabilitiesResponse); ok {
			return resp.GetCapabilities().GetSupportedHookPoints()
		}
	case commonv1.Category_CATEGORY_TOOL:
		if resp, ok := capabilities.(*toolv1.GetSchemaResponse); ok {
			return resp.GetSupportedHookPoints()
		}
	case commonv1.Category_CATEGORY_CONTEXT:
		if resp, ok := capabilities.(*contextv1.GetCapabilitiesResponse); ok {
			return resp.GetCapabilities().GetSupportedHookPoints()
		}
	case commonv1.Category_CATEGORY_MEMORY:
		if resp, ok := capabilities.(*memoryv1.GetCapabilitiesResponse); ok {
			return resp.GetCapabilities().GetSupportedHookPoints()
		}
	case commonv1.Category_CATEGORY_FRONTEND:
		if resp, ok := capabilities.(*frontendv1.GetCapabilitiesResponse); ok {
			return resp.GetCapabilities().GetSupportedHookPoints()
		}
	case commonv1.Category_CATEGORY_WIDGET:
		if resp, ok := capabilities.(*widgetv1.GetCapabilitiesResponse); ok {
			return resp.GetCapabilities().GetSupportedHookPoints()
		}
	case commonv1.Category_CATEGORY_SLASHCOMMAND:
		if resp, ok := capabilities.(*slashcommandv1.GetCapabilitiesResponse); ok {
			return resp.GetSupportedHookPoints()
		}
	}
	return nil
}
