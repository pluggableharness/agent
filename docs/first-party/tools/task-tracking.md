# Task Tracking / Todo Lists

## What it is

Task tracking covers tools that let the model (or the harness on the model's behalf) maintain an explicit, structured record of the sub-steps of a multi-step piece of work — as distinct from the model just reasoning about next steps in free text. The record typically carries at minimum an item's text/title and a status (`pending`/`in_progress`/`completed`, sometimes `cancelled`/`blocked`), and is displayed back to the user (a checklist in the chat UI, a rendered todo panel) as a progress indicator.

Two structurally distinct patterns are in common use, not just naming variance on one idea:

- **Persistent structured lists with a dependency graph** — tasks are first-class objects with an ID, created/updated/queried through separate CRUD-shaped operations, and can declare dependencies on each other. This is closer to a lightweight project-management data model than a checklist.
- **Session-scoped checklists** — a single tool call replaces or overwrites the entire todo list as an in-memory array for the current session; no persistence beyond the conversation, no cross-item dependency relationships.

The capability sits squarely in the "agent self-management" layer of a coding harness's workflow: it doesn't touch the filesystem, network, or shell, and its only externally visible effect is what it renders to the user and what context it feeds back to the model on subsequent turns (e.g. "you have 2 pending, 1 in_progress task"). It's frequently paired with — but distinct from — plan mode (see `plan-mode.md`): plan mode gates *writes* until a plan is approved, whereas task tracking is a running scratchpad of *progress* against work already underway.

## Design considerations

**Full-replace vs. partial-update semantics.** A checklist-shaped tool commonly re-sends the entire current list on every call rather than exposing a partial-update RPC; a CRUD-shaped tool instead exposes separate create/update/get/list operations addressed by task ID. The CRUD shape scales better to dependency tracking; the checklist shape is simpler to implement and sufficient for a flat progress list.

**Status vocabulary and invariants.** A common minimum is `pending`/`in_progress`/`completed`; richer designs add `cancelled`/`blocked` and may enforce an invariant like "at most one `in_progress` task at a time" to keep the rendered checklist meaningful.

**Persistence scope is a data-freshness question, not a permission one.** Whether the list survives context compaction or a session boundary matters for whether a stale task list can outlive the work it described — no widely-used design documents an explicit expiry or invalidation mechanism for this.

## Permission, sandbox & risk classification

Task tracking maps to three operations, classified as `task_create` (kind `resource`, risk `low`), `task_update` (kind `resource`, risk `low`), and `task_list` (kind `data_source`, risk `read_only`) — see [`reference-catalog.md`](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls) for the reasoning. Create/update are `resource` because they mutate state (a task list), but that mutation's blast radius is entirely internal to the harness — no external system is touched — which is why `low` rather than `moderate` risk applies. That combination is deliberately friendly to an `agent.hcl` policy that auto-approves the whole `task` provider, mirroring the read-auto-approval pattern already common for pure data sources.

No sandboxing applies here: the operation neither reads secrets, touches the filesystem, executes code, nor reaches the network, so none of the usual OS-level isolation mechanisms are relevant.

A reference `task` provider that only supports a flat create/update/list shape (no dependency graph) is a reasonable minimal implementation; a provider wanting a full dependency graph and a visualization of it would need to extend the operation set (e.g. an additional `task_add_dependency` operation) rather than finding it already scaffolded.
