# Grep / Content Search

## What it is

Grep/content search is the capability that lets an agent search *inside* file contents for a regex or literal pattern, as distinct from glob/path search (which matches file *names*). It answers "which files, and where, contain X" — the standard first move for locating a symbol definition, a string literal, a config key, or all call sites of something before an edit. It sits alongside glob search and file read as the trio a coding agent uses to build a mental map of a codebase before mutating it.

Content search is read-only by design: it never writes, and its only meaningful side effect is CPU time and, where output is capped, truncation. Its main design questions are narrower than for mutating tools: what regex dialect to expose, what scoping (glob/language/path) to support, how to bound result size, and whether to fold in a semantic/conceptual search variant alongside the literal one.

Grep is commonly paired with glob and read into a triad that a sub-agent or "search mode" wraps for exploratory work, rather than routing every lookup through the main model's own tool calls — a pattern worth noting for a `spawn_subagent` composition story.

## Common implementation patterns

Content search converges strongly on the name `grep` (or `grep_search`), typically implemented as a native function-calling tool that shells out to ripgrep or wraps an equivalent regex engine, offering output modes such as files-with-matches, content, or count, multiline matching, and `.gitignore`-aware scoping by glob or language type.

Output shaping diverges more than naming: some implementations sort matches by file modification time; others cap the number of returned matches to bound context growth (a hard ceiling in the low hundreds is a common choice).

A related but distinct pattern layers a semantic/conceptual search tool alongside the literal regex tool — either as a second dedicated tool gated behind an experimental flag, or as a sub-agent that chains grep, glob, and read calls to answer behavioral queries a raw regex can't. This composes naturally with subagent delegation rather than requiring a new primitive baked into the core search tool.

Some harnesses fold content search into a general-purpose shell/exec tool instead of exposing a dedicated function — content search, in that shape, is just a shell-invoked `grep`/`rg` rather than a first-class operation. Harnesses without any native tool-calling loop at all (relying instead on prompt-injected text edit formats) have no agent-invocable search primitive; a user runs `grep` manually outside the loop.

## Permission, sandbox & safety

Content search is the least-gated tool category in common use — treated as low-risk because it has no write path and the same blast radius as reading files the agent could already read.

- **No-approval tier.** Content search typically runs without approval friction, placed in the same free-to-run bucket as glob and file read.
- **Plan-mode inclusion.** Wherever a harness has a plan/read-only mode, grep is in the allowed subset — consistent with its read-only nature making it safe under any gate designed to block writes while allowing exploration.
- **Sandboxing is largely orthogonal.** OS-level sandboxing is aimed at the shell/exec tool, where arbitrary commands (including a shelled-out `rg`) run; a dedicated `grep` tool is not typically treated as its own sandbox-scoped case separate from the read-only permission tier.
- **Risk profile stays low even at scale.** The one operationally distinguishing factor is result-size bounding rather than access control — the practical risk of a grep tool is resource/context exhaustion from an unbounded result set, not unauthorized access, since it can only surface content the agent already has read permission over.

## Design considerations

Every implementation that has a dedicated grep tool treats it as read-only and auto-approved (or auto-approved within the workspace), places it in whatever plan/read-only mode exists, and names it `grep` or a close variant — content search has essentially none of the naming or mechanism divergence seen in file-edit tools. It's treated, functionally, as a sibling of glob and read_file rather than its own risk category.

Where implementations do diverge: (1) whether grep is a first-class tool at all versus folded into shell exec; (2) whether a semantic/conceptual search sibling exists alongside the literal regex tool; (3) output shaping conventions (mtime-sorted vs. unsorted, mode selection, hard caps), which vary without any apparent convergence — this looks like independent design rather than a contested question.

## Implications for PluggableHarness Agent

Grep maps directly onto the reference catalog's [`search`/`grep`](../../specifications/tool/reference-catalog.md) entry: `kind: data_source`, `risk: read_only`. No common implementation treats grep as riskier than a read, and several place it in a zero-approval tier, which validates `read_only` risk rather than merely `low`.

The reference catalog also recommends the implementation shell out to ripgrep — directionally sound engineering guidance regardless of exactly how many other implementations use ripgrep internally, since ripgrep's `.gitignore`-awareness, multiline support, and speed are independently good reasons to choose it for a reference implementation.

Pairing literal grep with either a semantic-search tool or a sub-agent wrapper for conceptual queries composes naturally as a third-party `search` provider operation (e.g. `semantic_search`) or as ordinary `spawn_subagent` usage, rather than requiring a change to the reference `grep` operation's own schema — consistent with the reference catalog's general posture that the common-core capabilities cover the convergent cases and leave differentiators to third-party or composed providers.

Grep's classification is settled and uncontroversial, unlike the `bash`/`exec` and `web_fetch` [ambiguous classification calls](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls).
