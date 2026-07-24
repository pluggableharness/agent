# Glossary

Terminology used throughout `docs/specifications/`.

| Term | Meaning |
|---|---|
| **Provider** | A plugin binary implementing one of the six categories: model, tool, memory, context, frontend, widget. |
| **Category** | One of the six provider kinds above, each with its own protocol (`model/`, `tool/`, `memory/`, `context/`, `frontend/` — widget is documented alongside frontend). |
| **Resource** | A tool operation that **mutates** state — gated behind the plan/apply flow. See [`agent-loop/plan-apply-gate.md`](agent-loop/plan-apply-gate.md). |
| **Data source** | A tool operation that only **reads** — executes freely (subject to a policy precheck, not a plan/apply gate), feeds the plan. |
| **Interactive** | A tool kind for calls that neither read nor write state but require a human response mid-turn (e.g. `ask_user`). See [`tool/protocol.md`](tool/protocol.md#kind-interactive) and [`agent-loop/plan-apply-gate.md`](agent-loop/plan-apply-gate.md). |
| **Plan** | The set of proposed resource (mutating) calls for a turn, shown as a diff for operator approval. |
| **Apply** | Executing an approved plan. |
| **Hook** | A named lifecycle point in the agent loop that plugins and first-party policy can subscribe to (`session-start`, `context-assemble`, `pre-model-call`, `post-model-response`, `pre-tool-call`, `post-tool-call`, `plan-ready`, `post-apply`, `session-end`). See [`agent-loop/hook-dispatch.md`](agent-loop/hook-dispatch.md). |
| **Hook mode** | How a hook subscriber participates: `observe` (read-only), `transform` (returns a modified payload for the next subscriber), or `veto` (can short-circuit with an explicit decision). |
| **Emit** | A plugin sending a raw, opaque-payload event into the kernel, persisted verbatim to the state backend. |
| **Event payload schema** | The `event.v1` message a given event kind's payload marshals to/from — schema_version `"1"` of that kind. Normative for the emitting owner, still opaque to the kernel at write time. See [`state-backend.md`](state-backend.md#the-kind-enum). |
| **Backfill** | The unicast replay a frontend receives on attaching to a session: persisted events re-rendered in sequence order through the supersedes path, bracketed by `SessionAttached` and `BackfillComplete`. See [`frontend/frontend-protocol.md`](frontend/frontend-protocol.md). |
| **Render** | A producer plugin turning its own previously-emitted payload into a display-agnostic `RenderTree`, on request from the kernel. |
| **Paint** | A frontend plugin turning a `RenderTree` into actual pixels/text/audio. |
| **RenderTree** | The display-agnostic intermediate representation every `Render` call returns — formally defined in [`frontend/render-tree.md`](frontend/render-tree.md) and shared verbatim by every category's `Render` RPC. |
| **Supersedes** | Version-pinned replay: an old event renders via the *exact* plugin version that produced it (`producer.version`), never a provider-authored upgrade function. |
| **Producer** | The `{category, name, version}` identity attached to an emitted event, a log line, or a span — the thing a `RunSession`/`Log`/`Emit` call is attributed to. See [`kernel-callbacks.md`](kernel-callbacks.md). |
| **RunSession** | The kernel callback primitive that runs a full agent session — used for sub-agent spawns today, and reserved for a future non-interactive pipeline mode. See [`agent-loop/subagents.md`](agent-loop/subagents.md) and [`kernel-callbacks.md`](kernel-callbacks.md). |
| **CountTokens** | The kernel callback primitive that resolves an exact-if-possible token count for a string, preferring a model provider's real tokenizer and falling back to a canonical heuristic only as a last resort. See [`kernel-callbacks.md`](kernel-callbacks.md). |
| **Policy** | The first-party, kernel-privileged rule-matching DSL in `agent.hcl` that decides `allow`/`ask`/`deny` for resource, data-source, and interactive calls — mechanically a `veto`-mode subscriber at the `plan-ready` hook. See [`configuration/policy-dsl.md`](configuration/policy-dsl.md). |
| **Agent profile** | A named, scoped capability set (model, tools, policy overrides, depth budget) in `agent.hcl` that a sub-agent spawn selects, rather than inheriting the parent session's full unscoped capabilities. See [`configuration/agent-profiles.md`](configuration/agent-profiles.md). |
| **Depth budget** | The remaining sub-agent nesting allowance threaded from a profile's configured maximum, decremented per `RunSession` hop, distinct at the root (kernel default) vs. a child (inherited). |
| **State backend** | The kernel-owned, non-pluggable (in v1) persistence layer — sqlite-per-session — recording every event, cost figure, and plan item. The kernel is its sole writer. See [`state-backend.md`](state-backend.md). |
| **Schema-to-cty bridge** | The mechanism translating a provider's declared config schema into an `hcldec` spec so `agent.hcl` provider blocks decode through real HCL2/`cty`, distinct from the JSON-Schema subset tool authors use for LLM function-calling. See [`configuration/blocks-reference.md`](configuration/blocks-reference.md). |
| **Canonical message** | The kernel's internal content-block message representation (`text`, `tool_use`, `tool_result`, `image`, `thinking`, `redacted_thinking`) — the state backend's source of truth, independent of any one vendor's wire format. See [`model/data-types.md`](model/data-types.md). |
| **Lock file** | `.agent/agent.lock.hcl` — pins resolved provider version, source, and checksum per provider, mirroring `.terraform.lock.hcl`. See [`configuration/lock-file.md`](configuration/lock-file.md). |
