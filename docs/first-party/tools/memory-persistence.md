# Memory / Cross-Session Persistence

## What it is

Memory / cross-session persistence is the family of mechanisms by which a coding harness carries knowledge — facts about the user, prior decisions, project state, past conversation content — forward across sessions that would otherwise start from a blank context window. It sits outside the ordinary read-act-observe loop: rather than operating on the current repository state (files, shell, search), it operates on the harness's own accumulated experience of working with a given user or project over time.

This label commonly covers at least three distinct behaviors:

1. **Durable fact storage** — a structured, named record (a memory) written once and recalled in later sessions.
2. **Session-history search/replay** — indexing or re-reading *past conversation transcripts themselves*, not extracted facts.
3. **Persistent instruction/rule files** — text conventions that shape future sessions' system prompts, adjacent to memory but really a context-assembly mechanism.

All three are commonly surfaced to the model as an ordinary callable tool, which is the crux of the design discussion below, since PluggableHarness Agent gives this capability a dedicated provider category instead.

## Common implementation patterns

**Naming and scope conventions diverge sharply** — more so than most capabilities:

- The most "tool-like" implementations expose a single tool with an action parameter (create/update/delete), workspace-level scoping, and a hard requirement that the model check for existing memories before creating a new one to avoid duplicates — a deduplication discipline PluggableHarness Agent's own protocol independently arrives at via its fuzzy near-match check.
- Some implementations instead decompose the same operation set into several separate tools (remember/remove-category/remove-specific, plus category tagging), supporting both local-project and global-user scopes.
- Others lean on semantic/keyword search rather than structured fact records — closer to a search index over accumulated text than a curated memory taxonomy, sometimes explicitly spanning multiple working-tree checkouts of the same project.
- **Session-history replay vs. fact extraction** is the sharpest semantic split: some implementations search *raw past transcripts*, while others store *distilled facts* the model (or harness) chose to write. These are not interchangeable — transcript search finds anything that was ever said; fact storage only finds what was deliberately persisted.
- **Persistent rule files** are architecturally distinct from both of the above — they don't recall content into context by search or relevance ranking at all; they write a rule file that gets picked up by the harness's ordinary context-assembly/system-prompt mechanism on future sessions. This is functionally closer to a context-provider concern than to a recall-and-rank memory model.
- Some harnesses have no dedicated tool at all: their "memory" is entirely emergent — git commit history as an implicit record of past decisions, plus explicit user-driven session-transcript save/load commands rather than model-invocable ones. This is closer in spirit to session persistence-as-infrastructure than to something the model calls a tool to invoke.
- Reliability varies: some memory tools are recent additions with thin adoption signal; transcript-search features are sometimes shipped disabled by default and must be explicitly enabled.

## Permission, sandbox & safety

Memory-as-a-tool inherits whichever general permission model its host harness uses — no common implementation applies a distinct, memory-specific approval tier beyond what governs its other resource-shaped tools. A create/update memory tool is typically gated the same way other mutating tools are gated; a content-level safeguard (a mandatory dedup check before writing) is common as a behavioral guard against silent duplication, distinct from any approval gate. Read-only recall/search operations typically fall under the same general trust model as other read tools, sometimes explicitly labeled experimental.

No implementation sandboxes memory storage at the OS level — unsurprising, since writing/reading a local memory record has no shell, filesystem-outside-project, or network surface comparable to `bash` or `web_fetch`. The real risk this capability carries is **not execution risk but data-integrity and privacy risk**: an incorrect or stale memory record persists silently across sessions and can misinform the model far into the future, and duplicate/conflicting records degrade quality over time if not actively curated. Any tool capable of writing durable state is also a plausible target for injected instructions to abuse — e.g. tricking the model into "remembering" attacker-controlled content that resurfaces in later sessions; mitigation beyond ordinary write-approval gating is rarely documented.

## Design considerations

Every implementation that goes beyond raw transcript search treats memory as named, categorized entries rather than an undifferentiated blob — dedup-before-create, tagged categories, and a fixed taxonomy all converge on the idea that unstructured memory degrades badly at scale and some organizing principle is required. Every implementation surfaces memory read/write as an ordinary model-invocable tool, with none treating it as a privileged, harness-only mechanism analogous to how sandbox enforcement is treated.

Where the field splits: fact storage, transcript search, and rule files are three genuinely different capabilities commonly grouped under one label, with little cross-pollination — few implementations cleanly combine more than one. Adoption is on-by-default in some places and opt-in in others; several mature tool surfaces have no memory capability at all, suggesting this is treated as a nice-to-have differentiator rather than baseline expected behavior. There's no standard scope model (workspace vs. local-project/global-user vs. explicitly worktree-spanning), and no cross-reference/linking convention comparable to a structural link between records — memory records are typically treated as independent entries, not a graph.

## Implications for PluggableHarness Agent

This capability does **not** belong to the [tool reference catalog](../../specifications/tool/reference-catalog.md), which excludes memory-as-a-tool as largely redundant with the memory *provider* category and leaves it to future/third-party providers rather than the first-party reference set. That framing holds up well against common practice — the picture above shows genuine fragmentation (three different behaviors under one label, no dominant naming or scope convention, several major tool surfaces skipping it entirely), which is exactly the kind of immature, differentiator capability the reference catalog reserves for third parties rather than standardizing prematurely.

The capability that *does* exist as first-party, protocol-level design is the [memory provider](../../specifications/memory/README.md)'s dedicated `Recall`/`Record`/`UpdateRecord`/`DeleteRecord` RPCs — a different plugin protocol from the tool-provider one entirely, not a tool operation. Where common practice exposes memory purely as a model-callable tool, the memory provider protocol architecturally separates the mechanism into two paths that together cover more ground than any single implementation above:

- An **autonomous, hook-driven write path** ([`memory/protocol.md#write-triggers`](../../specifications/memory/protocol.md#write-triggers), subscribed to `post-response`/`session-end`) with no common analogue — most implementations require an explicit model tool call to persist anything; passive, turn-boundary-triggered writes are rare.
- An **explicit, model-invoked reference-tool path** (`memory.remember`/`memory.forget`/`memory.search`) that *is* implemented as an ordinary tool-shaped operation (`kind: resource`, `risk: moderate` for remember/forget; `kind: data_source`, `risk: read_only` for search) — this is the point of contact between the two specs: the same plugin process implements both the memory-provider RPCs *and* registers as a tool provider for these three operations, calling directly into its own `Record`/`DeleteRecord`/`Recall` methods rather than crossing a plugin boundary.

Two patterns above materialized independently as protocol-level rules, worth noting as convergent validation:

- The "must check for existing memories before creating" discipline is exactly what [`memory/protocol.md#write-triggers`](../../specifications/memory/protocol.md#write-triggers) formalizes as `memory.remember`'s mandatory fuzzy near-match check before creating a new record (returning a `result`, not an error, so the model resolves the ambiguity on its next turn) — common practice shows this as prose guidance baked into one tool's description; PluggableHarness Agent elevates it to a MUST at the protocol level.
- A local-project/global-user scope split is a rougher, two-tier version of [`memory/data-types.md#memoryscope`](../../specifications/memory/data-types.md#memoryscope)'s three-tier `session`/`project`/`global` split — no common implementation has PluggableHarness Agent's session-scoped tier, which PluggableHarness Agent motivates as memory that's durably logged to the [state backend](../../specifications/state-backend.md) for audit but deliberately excluded from ordinary cross-session recall.

One divergence worth flagging: persistent rule files and implicit git-history memory don't map cleanly onto the memory provider protocol at all — the former is closer to a [context provider](../../specifications/context/README.md) concern (a persistent instruction file that shapes future system prompts, not a recall-and-rank fact store), and the latter is closer to the state backend's session persistence/replay. This isn't a gap in the memory protocol so much as confirmation that "memory" as commonly used maps onto at least three different PluggableHarness Agent plugin categories depending on which behavior a given implementation actually provides — worth keeping in mind if a future third-party tool provider proposes a "memory" tool that turns out, on inspection, to actually be a context provider or a state-backend feature in disguise.
