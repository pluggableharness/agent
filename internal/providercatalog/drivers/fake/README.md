# internal/providercatalog/drivers/fake

The scripted, in-memory `providercatalog.Catalog` for tests — a hand-written fake per `.claude/rules/go-testing.md`, not a generated mock.

## What it is for

Every agent-loop package (turn driver, hook dispatch, plan/apply gate, tool scheduler, model caller, context assembler) unit-tests against this one type: declare which providers are "loaded", then run the real logic with no subprocess, no gRPC dial, and no plugin binary on disk.

## Building a scenario

```go
cat := fake.New().
	AddModel(agentprofile.ModelRef{Provider: "anthropic", ID: "claude-opus-4"},
		providercatalog.ModelHandle{Spec: spec, Client: modelClient}).
	AddTool("fs", "read_file", providercatalog.ToolHandle{Schema: schema, Client: toolClient}).
	AddTool("fs", "write_file", providercatalog.ToolHandle{}).
	AddContext(providercatalog.ContextHandle{Provider: "git_status", Client: ctxClient}).
	AddHook("fs", providercatalog.HookHandle{Client: hookClient})
```

Each `Add` takes the lookup key as a parameter and stamps it into the handle (`Ref`, `Provider`, `Position`), so a scenario names a provider once and cannot register a handle under a key that disagrees with its own fields. Adders are chainable and replace any handle already under the same key.

The fields are also exported, so a scenario the adders cannot express builds a struct literal instead — the usual reason is non-sequential `ContextHandle.Position` values:

```go
cat := &fake.Catalog{ContextProviders: []providercatalog.ContextHandle{
	{Provider: "second", Position: 3},
	{Provider: "first", Position: 0},
}}
```

`Contexts()` sorts by `Position`, so slice order in a literal does not matter. The zero `Catalog` is usable: every lookup reports `ErrNotFound`, which is what a "nothing is loaded" scenario wants, and the `Add` methods work on it too.

## Guarantees a consumer's tests can rely on

- Every lookup miss returns an error wrapping `providercatalog.ErrNotFound`, naming what missed.
- `ToolNames()` sorts each provider's operation names, so an assertion on the result never depends on map iteration order.
- `ModelSpecs()`, `ToolNames()`, and `Contexts()` each return a freshly built map or slice — a caller may retain or mutate the result without disturbing the catalog.
- `TestComposesWithAgentprofile` feeds `ModelSpecs()` and `ToolNames()` into the real `agentprofile.SelectModel` and `agentprofile.ResolveTools`, so the map shapes are proven to compose rather than merely to look right.

## What it does not do

It validates nothing and dials nothing. A `ToolHandle` with a nil `Schema`, or a `ModelHandle` whose `Spec` disagrees with its `Ref`, round-trips exactly as scripted — testing how a consumer copes with a malformed handle is a legitimate use of this fake, so it must be able to hold one.
