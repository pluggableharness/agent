# Specifications

The authoritative protocol and kernel-contract documentation for `PluggableHarness Agent`. This directory is the source of truth for anything it covers.

Start with [`conventions.md`](conventions.md) — it defines the requirement keywords and the anchor-only cross-reference rule every other file follows. Then [`glossary.md`](glossary.md) for terminology and [`architecture.md`](architecture.md) for the system-level narrative (microkernel philosophy, the seven provider categories, Emit→Render→Paint, transport, config, registry, sub-agents, policy, hook dispatch).

## Reading order

1. [`conventions.md`](conventions.md) — how to read everything else.
2. [`glossary.md`](glossary.md) — terminology.
3. [`architecture.md`](architecture.md) — the system narrative.
4. The seven plugin-category protocols (any order — cross-linked as needed):
   - [`model/`](model/README.md) — model (LLM vendor) provider.
   - [`tool/`](tool/README.md) — tool provider (resource / data_source / interactive).
   - [`context/`](context/README.md) — context provider.
   - [`memory/`](memory/README.md) — memory provider.
   - [`frontend/`](frontend/README.md) — frontend provider **and** widget provider.
   - [`slashcommand/`](slashcommand/README.md) — slash-command provider.
5. The kernel's own required behavior, not a plugin protocol:
   - [`agent-loop/`](agent-loop/README.md) — the turn loop, hook dispatch, plan/apply, sub-agents.
   - [`configuration/`](configuration/README.md) — `agent.hcl`, the policy DSL, agent profiles, global config, the lock file.
   - [`kernel-callbacks.md`](kernel-callbacks.md) — the plugin→kernel direction (`RunSession`, `CountTokens`, `Emit`, `Log`).
   - [`state-backend.md`](state-backend.md) — session persistence and replay (sqlite, kernel-built-in, not pluggable in v1).

## What's not here

`docs/first-party/tools/` — the first-party tool catalog, maintained separately. Not part of this protocol documentation; don't confuse it with [`tool/`](tool/README.md) (singular), the tool provider *protocol*.

## Status

Every category and kernel-behavior document describes a `draft v1` contract.
