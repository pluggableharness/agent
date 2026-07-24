# Architecture

The kernel is a **microkernel**: it provides almost no functionality of its own, only primitives — plugin lifecycle, hook dispatch, the plan/apply gate, config loading, and a state backend — and everything with an opinion is a plugin. This document is the architecture narrative; the per-category protocol documents are the normative contract. Where this document and a category document disagree, the category document wins — this file is orientation, not the source of conformance requirements.

This is a deliberate fusion of two lineages: Terraform's plugin/provider/ schema/registry/plan-apply model, and a Neovim/VSCode-style hook-and- extension-point system for behavior injection — Terraform itself has no equivalent of the latter. See [`glossary.md`](glossary.md) for terminology.

## The six provider categories

Six categories share a common shape: `GetSchema`/`GetCapabilities` (declare what you do), `Configure` (accept config decoded from HCL), then category-specific RPCs.

- **Model provider** ([`model/`](model/README.md)) — an LLM vendor. `GetCapabilities` returns a quantitative envelope per model (context window, thinking/caching modes, pricing tiers), not just feature flags. `StreamCompletion` is **server-streaming with cancellation**, not bidirectional — this matches how a real LLM vendor API actually works: one request in, one chunked/SSE response out; cancellation is the kernel closing the stream, a standard server-streaming operation. See [`model/README.md`](model/README.md#transport--lifecycle).
- **Tool provider** ([`tool/`](tool/README.md)) — `GetSchema` returns resources/data-sources/interactive calls, each with a JSON-Schema input/ output and a `kind`. `Invoke` is server-streaming (so e.g. `exec` can stream live stdout instead of blocking).
- **Memory provider** ([`memory/`](memory/README.md)) — reads at `context-assemble` (inject relevant recall), writes at `post-response`/ `session-end` (decide what's worth persisting). Backend-agnostic (markdown files, sqlite, vector store, remote service) behind one interface — the same abstraction-over-backend move Terraform makes for state.
- **Context provider** ([`context/`](context/README.md)) — hooks `context-assemble`, contributes text/data before each turn. Multiple can load simultaneously (a CLAUDE.md reader, an AGENTS.md reader, etc.) — sidesteps the convention-file format war entirely; it's a plugin choice, not a core opinion.
- **Frontend provider** ([`frontend/`](frontend/README.md)) — one genuinely bidirectional stream: kernel emits state events (token deltas, plan-ready, permission-request, tool output), frontend emits client events (user message, plan approve/reject/edit, interrupt). Multiple frontends may attach to one session — see [`frontend/README.md`](frontend/README.md#session-scope--multi-attach).
- **Widget provider** ([`frontend/widget-protocol.md`](frontend/widget-protocol.md)) — derives persistent display state from the same event stream a frontend already sees; no new data feed, server-streaming only.

## Emit → Render → Paint pipeline

Three hops, and this is why "render" doesn't need reimplementing per frontend:

1. **Emit** — a producer plugin sends a raw payload event into the kernel; logged verbatim to the state backend. The payload is deliberately **opaque** — the kernel never inspects it. This is the one sanctioned exception to this system's otherwise strongly-typed wire contracts.
2. **Render** — the kernel calls back into the *producing* plugin — `Render(payload, schema_version) → RenderTree`, a display-agnostic IR (text runs, code blocks, diffs, tables, links, a "sub-session" node type for nested agent transcripts, an `action` node for interactive widgets). `Render` is optional per plugin; the kernel falls back to a generic default (pretty-print / raw text field) if a producer declines. See [`frontend/render-tree.md`](frontend/render-tree.md).
3. **Paint** — the kernel hands the `RenderTree` to whichever frontend(s) are attached; each paints it however it wants (ANSI TUI, HTML, voice). Frontends never need producer-specific knowledge.

Because replay is just "feed old events through the same Render/Paint path against the state backend instead of the live loop," there is no separate history-viewer subsystem.

## Versioning & schema drift — "supersedes"

Rather than provider-authored upgrade functions (Terraform's `UpgradeResourceState` pattern), old events are rendered by spinning up the *exact plugin version* that produced them (recorded per-event as `producer.version`). Simpler for plugin authors, perfect fidelity, no migration-function API surface to get wrong.

Consequence: the plugin cache must be **session-log-aware**, not a naive LRU/TTL. A version is only eligible for eviction once no retained session references it — otherwise a retained session can silently become unrenderable, permanently so if the upstream release was also deleted. Pruning is an explicit operator command, never implicit background GC.

## Transport

`hashicorp/go-plugin` — subprocess + gRPC, versioned handshake — for every category. Chosen over Go's native `plugin` package specifically because third-party contribution is a goal: go-plugin gives crash isolation, independent release cycles per provider, and any language can implement a provider. This is the literal library Terraform itself uses. The kernel-side launch sequence is eight steps: a preflight version check, an env-allowlisted subprocess launch (a plugin subprocess receives no ambient environment beyond a minimal, explicit allowlist — secrets are never inherited from the kernel's own environment), the handshake, a negotiated-version gate, category-client construction, the broker callback-channel service, and a drain-then-kill shutdown.

The callback channel (plugin → kernel direction) uses a **fixed, well-known broker ID**, not a wire-negotiated one — safe because the kernel is the only party that ever accepts a connection on it, so no collision is possible.

## State backend

Kernel-owned, not pluggable in v1 (sqlite-per-session locally; a remote/ shared backend is future work, same reasoning as Terraform's backend split — local-only breaks the moment a team wants to share session history). The kernel owns exactly one thing here — the **event envelope** — and never looks inside `payload`. See [`state-backend.md`](state-backend.md) for the full schema.

This is what makes "you'll need to install X to re-render this" possible: on load, walk the event log, collect the distinct `producer` set, diff against what's installed/cached, report what's missing before touching anything — the same move as `terraform init` reporting missing providers before `plan`.

## Config — `agent.hcl`, full HCL2

Real `hashicorp/hcl` v2, `cty` types — not a bastardized subset. Scope is deliberately narrower than Terraform's HCL: there is no static resource graph to plan against, because the LLM decides tool call order/content at runtime, not an `.hcl` author. See [`configuration/`](configuration/README.md) for the full block reference (`required_providers`, `provider`, `policy`, `agent_profile`, `settings`, global config, the lock file).

## XDG layout

| Path | Contents |
|---|---|
| `./agent.hcl` (+ other `*.hcl` in project dir, merged) | root config |
| `./.agent/` | project-local lock file (`agent.lock.hcl`) — resolved versions + checksums |
| `$XDG_CONFIG_HOME/agent/` | global CLI config — credentials, dev overrides, registry mirrors |
| `$XDG_CACHE_HOME/agent/` | downloaded plugin binaries, keyed by name/version/platform/checksum |
| `$XDG_DATA_HOME/agent/` | persistent plugin data — memory-provider storage if file-backed |
| `$XDG_STATE_HOME/agent/` | session state/transcripts, plan/apply audit logs |

## Registry & distribution

Direct git-forge resolution, **not** a central index (a discovery portal is deferred future work). Consistent with how Terraform already treats *modules* (arbitrary `source = "github.com/..."`) as opposed to *providers* (registry-gated) — this project applies the module pattern to provider-shaped plugins. Source of truth is the repo; no publish step beyond `git tag`.

```hcl
required_providers {
  anthropic = {
    source  = "github.com/agentco/provider-anthropic"
    version = "~> 1.2.3"
  }
}
```

- Version listing via GitHub/GitLab Releases APIs, tags assumed semver (`v1.2.3`).
- Same constraint operators as Terraform: `=`, `!=`, `>`, `>=`, `<`, `<=`, `~>`.
- Asset naming convention matches goreleaser defaults (`provider-anthropic_1.2.3_linux_amd64.tar.gz`).
- Checksum file per release, verified before caching; `GITHUB_TOKEN`/ `GITLAB_TOKEN` for private repos, no new credential store.
- Lock file `.agent/agent.lock.hcl` pins resolved version + checksum + source per provider — see [`configuration/lock-file.md`](configuration/lock-file.md). The lock file's own version field is checked, and a too-new lock file rejected outright, before anything else in it is read — the same migration-safety posture the state backend applies to its own schema version; see [`state-backend.md`](state-backend.md#schema-migration).
- **Discovery**: deferred to a future portal. Interim: a topic-tag convention (e.g. `agent-harness-provider`) so `gh search`/`glab search` work today at zero cost.

## Sub-agent support

Kept honest to the microkernel: not privileged kernel code. The kernel exposes a callback primitive, `RunSession(profile, prompt, scoped_providers) → result`, available to *any* plugin. "Spawn a sub-agent" is then an ordinary tool provider (even a third-party one) whose `Invoke` calls back into that primitive. See [`agent-loop/subagents.md`](agent-loop/subagents.md) and [`kernel-callbacks.md`](kernel-callbacks.md).

- **Session hierarchy** — child sessions carry `parent_session_id` in the state backend so replay can reconstruct the tree.
- **Scoped capability profiles** — `agent.hcl` defines named sub-agent profiles (model provider, tool providers, policy overrides, depth budget); spawn picks a profile rather than inheriting the parent's full capability set unscoped. See [`configuration/agent-profiles.md`](configuration/agent-profiles.md).
- **Concurrency** — parallel sub-agent spawns need a scheduler cap and state-backend writes tolerant of concurrent child sessions.
- Reserved for later: the same mechanism a future non-interactive "pipeline" CLI mode would reuse — entering `RunSession` from the CLI non-interactively with an auto-resolving policy instead of prompting. Explicitly out of scope for v1; not new architecture when it arrives.

## Policy — first-party, not a plugin category

Ties directly to the plan/apply gate (kernel-owned). Lives in `agent.hcl` as a small rule-matching DSL, deliberately mirroring a shape already proven out in practice (Claude Code's own `settings.json` allow/deny + auto-mode classifier). Mechanically, policy is the kernel-privileged `veto`-mode subscriber at the `plan-ready` hook — always run, always respected, and not itself a plugin call (it does not go through `HookSubscriberService`). Third-party plugins MAY also register `veto`-mode hooks, `agent.hcl` declaration being the operator's trust grant to do so; see [`agent-loop/hook-dispatch.md#veto-mode-subscription-trust-model`](agent-loop/hook-dispatch.md#veto-mode-subscription-trust-model). See [`configuration/policy-dsl.md`](configuration/policy-dsl.md) for the full DSL and evaluation semantics, including conflict-detection.

## Hook dispatch semantics

Hook points (`session-start`, `context-assemble`, `pre-model-call`, `post-model-response`, `pre-tool-call`, `post-tool-call`, `plan-ready`, `post-apply`, `session-end`) run as an **ordered chain**. Each subscriber declares a mode:

- `observe` — read-only, can't alter payload or veto (logging/audit).
- `transform` — receives the previous stage's output, returns a modified version; the next subscriber sees the transformed payload (context providers at `context-assemble`).
- `veto` — can short-circuit with an explicit decision (policy at `plan-ready`).

Ordering within a hook is declaration order in `agent.hcl`, not runtime registration order — determinism matters especially for `context-assemble`, where order affects what the model attends to.

The wire surface for all eight dispatchable points other than `context-assemble` (which stays on `ContextService.Contribute`, per [`context/protocol.md#contribute-the-context-assemble-rpc`](context/protocol.md#contribute-the-context-assemble-rpc)) is `pluggableharness.hook.v1.HookSubscriberService` — one shared service every plugin category MAY implement, dispatched to over the same `hashicorp/go-plugin` connection as that plugin's own category service. See [`agent-loop/hook-dispatch.md`](agent-loop/hook-dispatch.md) for the full dispatch mechanics and wire contract.

## Canonical message / tool-schema format

Internal representation is content-block messages (`text`/`tool_use`/ `tool_result`/`image`/`thinking`/`redacted_thinking`) — the widest practical superset today. This is the state backend's source of truth, independent of whether any one vendor's wire format still exists at replay time. Each model-provider adapter owns its own lossy translation to/from vendor wire format. See [`model/data-types.md`](model/data-types.md).

Tool schemas: declared once per resource in a common JSON Schema subset all major vendors actually support (object/string/number/boolean/array/enum — skip exotic keywords like `oneOf`/`$ref` chains); each adapter translates to its vendor's tool-definition format. See [`model/data-types.md#tool-schema`](model/data-types.md#tool-schema).

## Context budget

Ceiling is **not** a config value — it's asserted at runtime from the resolved model provider's declared capabilities (`context_window − reserved_output − system_overhead`), computed fresh per call. A sub-agent routed to a smaller model gets a correspondingly smaller pool automatically.

Allocation policy (v1): fixed per-context-provider token caps declared in `agent.hcl`, validated against the dynamically-known ceiling at assembly time. Adaptive priority-based negotiation (providers asked to compress under pressure) is explicitly deferred — no adaptive machinery until there's evidence the fixed-cap approach is insufficient. See [`context/data-types.md`](context/data-types.md#budget-mechanics).

Generation-time parameters are validated the same way: an effort/thinking setting is checked against the resolved model's declared capabilities before it ever reaches the wire, and model routing/fallback chains are capability-aware for the identical reason — a candidate is only eligible for a turn if it actually satisfies that turn's real requirements (context needed, tool-use, vision, thinking), not merely because it's listed first. See [`model/protocol.md#generation-parameter-validation-and-capability-aware-routing`](model/protocol.md#generation-parameter-validation-and-capability-aware-routing) and [`configuration/agent-profiles.md#model-routing`](configuration/agent-profiles.md#model-routing).

## CLI shape

Single interactive `agent` command, UX mimicking Codex/Claude Code — not literal `init`/`plan`/`apply` verbs. The plan/apply mechanism stays internal, rendered inline as an approval prompt within one interactive session. A future non-interactive pipeline mode reuses `RunSession`; out of scope for v1.
