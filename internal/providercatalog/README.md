# internal/providercatalog

The read-only lookup interface every agent-loop package uses to reach a live plugin — and the only place those packages touch plugin lifecycle at all.

## What it is

`Catalog` answers four questions, and nothing else:

| Method | Answers |
|---|---|
| `Model(ref)` / `ModelSpecs()` | which model provider serves this `agentprofile.ModelRef`, and what every loaded model can do |
| `Tool(provider, tool)` / `ToolNames()` | which tool provider serves this `"<provider>.<tool>"` operation, and what every loaded provider advertises |
| `Contexts()` | every loaded context provider, in `agent.hcl` declaration order |
| `Hook(provider)` | a loaded plugin's `HookSubscriberService` client |

Every method is a pure lookup against already-resolved state. Nothing here launches a subprocess, runs a handshake, applies configuration, dials, retries, or tears anything down — all of that belongs to whatever future component *builds* a `Catalog`.

## Why it exists

The turn driver, hook dispatcher, plan/apply gate, tool scheduler, model caller, and context assembler all need the same handles and none of them needs lifecycle. Funneling all six through one narrow read-only interface makes that structural rather than conventional: an agent-loop package cannot start a plugin, because nothing it can reach exposes a way to.

The payoff is testing. Every one of those packages unit-tests against `drivers/fake` — real turn logic, scripted handles, zero subprocesses and zero gRPC. That is the unit tier's "in-memory fakes only" budget in `.claude/rules/go-testing.md`, satisfied by construction rather than by discipline.

## Handles

A handle bundles a dialed client with the static metadata the kernel already learned at load time, so a consumer never needs a round trip to make a routing decision:

- `ModelHandle` — `Ref`, `Producer`, `Spec` (what `agentprofile.SelectModel` reads), `Client`.
- `ToolHandle` — `Provider` (the `agent.hcl` local name), `Producer`, `Schema`, `Client`, plus two fields lifted out for the hot path: `SupportsPreview` (whether the plugin implements the optional `Preview` RPC — there is no schema field for it) and `TerminatesTurn` (mirrors `ToolSchema.terminates_turn`, consulted on every tool result).
- `ContextHandle` — `Provider`, `Producer`, `Capabilities`, `Client`, `Position` (declaration order), `TokenBudget` (the `agent.hcl` override if declared, else `Capabilities.DefaultTokenBudget`).
- `HookHandle` — `Producer`, `Client`, `SupportedPoints`.

Every lookup miss returns an error wrapping `ErrNotFound`, matched with `errors.Is`.

## Drivers

- `drivers/fake` — the scripted in-memory implementation. See its own README.
- `drivers/plugin` — **deliberately not built yet.** There is no real plugin registry to wrap, and writing a placeholder would fix this interface's shape against an imagined lifecycle API instead of a real one. It is a later phase's job, and it lands without changing this package.
