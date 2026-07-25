# internal/providercatalog/drivers/plugin

The real, production `providercatalog.Catalog` driver. It wraps a `*pluginhost.Registry` — every plugin `pluginhost.Supervisor.Start` has already launched, Described, checksum-verified, Configured, and registered for this session — and translates each `pluginhost.Live` into the resolved handle shapes `internal/turn`, `internal/session`, `internal/hookdispatch`, and `internal/contextassembly` consume.

This is the driver `internal/providercatalog/drivers/fake` stands in for in every other package's unit tests. A future `internal/kernel` composition root is the only place that constructs this driver for real, after `pluginhost.Supervisor.Start` has returned successfully.

## What it does

`New` builds a `Catalog` once, eagerly, from a fully-populated registry:

- Every model's `ModelSpec`, per model-category plugin.
- Every tool operation's `ToolSchema`, per tool-category plugin — including a one-time live probe of the optional `Preview` RPC to resolve `ToolHandle.SupportsPreview`, since the wire protocol carries no schema field for it.
- Every context provider's `ContextCapabilities` and effective `TokenBudget`, in agent.hcl declaration order.
- Every plugin's hook subscription, for any plugin whose `HookSubscriberService` client is reachable and which declared at least one supported hook point.

See `doc.go` for the full reasoning behind each of these — why extraction is eager, exactly how `SupportsPreview` is resolved, how `Position` relates to `pluginhost.Live.LaunchIndex`, and a documented gap around `ContextHandle.TokenBudget` never reflecting an agent.hcl override (the decoded provider config that override lives in isn't reachable from `pluginhost.Registry` today).

## Why it's read-only

`Catalog` never launches, configures, dials, or shuts down a plugin — `pluginhost.Supervisor` already owns that whole lifecycle. This package's only I/O is the one-time `Preview` probe `New` performs; every other method (`Model`, `ModelSpecs`, `Tool`, `ToolNames`, `Contexts`, `Hook`) is a pure map/slice read against state extracted once at construction.
