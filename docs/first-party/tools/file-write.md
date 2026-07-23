# File Write

## 1. What it is

File write denotes whole-file creation or full-content overwrite: the agent supplies a path and complete content, and the harness writes that content to disk, either creating a new file or replacing an existing one wholesale. It is the coarse-grained counterpart to file_edit (targeted in-place replacement of a substring or region) — where file_edit answers "change this part of an existing file," file_write answers "this file should now contain exactly this." In a coding agent's workflow it is the primitive behind scaffolding new files (configs, boilerplate, generated code, new modules) and behind any change large enough that a targeted diff isn't worth constructing, e.g. rewriting a file after a wholesale refactor or regenerating a lockfile.

Because it is destructive by construction — a write to an existing path discards its prior content outright — most harnesses pair it with some form of read-before-write discipline, and it sits toward the higher-risk end of file operations in most permission models, second only to shell execution and (where present) subagent spawning.

## 2. Adoption and mechanism

File write is near-universal for creating new files, though one notable implementation has no dedicated create-file primitive at all — new-file creation there has to go through the edit tool or shell, folding "create" into "edit" rather than treating it as its own operation. One other implementation covers whole-file rewrite only as one of several prompt-injected text formats rather than as a distinct write tool.

**Create vs. overwrite is usually one operation, not two.** Most named write tools handle both new-file creation and full overwrite of an existing file through the same call — the operation is distinguished by whether the target path exists, not by a separate flag or tool. A partial exception is an editor tool whose purpose spans three modes (replace old_text/new_text, create-if-missing, insert-at-line) in a single tool, blurring the file_write/file_edit line at the tool-surface level even though the underlying operations are conceptually distinct.

**No append/merge semantics.** Every mechanism observed is either full-content replacement or a rewrite framed as a diff/patch/block — file_write and file_edit are kept as cleanly separated concerns, with file_write reserved for "this is the file's new complete content" and partial changes routed through edit instead.

**Three distinct exposure mechanisms:**
- *Native function calling* (the majority pattern) — a dedicated `write`/`create_file`-style tool.
- *Freeform grammar tool* (a patch-envelope format, not JSON schema) — used specifically for whole-file writes rather than incremental edits, in harnesses that also use the same grammar for edits.
- *Prompt-injected text/XML* — a whole-file rewrite framed as one of several text edit formats parsed from plain prose, or an XML tag streamed and parsed server-side, with no native function calling involved.

**Model-gated tool routing** appears here too: some implementations route between a native write/edit tool and a grammar-constrained patch tool depending on which model is active — the file-write instance of a broader "route by model family" pattern that shows up across several capabilities.

One implementation is architecturally distinct from the rest: it has no runtime disk write at all until an explicit apply step — the write mechanism only stages content. This makes its file-write "coverage" conceptually different from the rest: the write operation itself is deferred and reviewed, not merely approved per-call.

## 3. Cross-tool variation

**Read-before-write as the dominant safety contract.** Several implementations explicitly enforce it: a write tool fails if overwriting an unread existing file, or requires reading the file first if it already exists. This is a harness-level invariant enforced independent of the permission system — it exists to prevent the model from blindly clobbering content it hasn't seen, which is a distinct concern from whether the user has approved the write. Other implementations instead auto-create missing parent directories with no read-before-write gate, or do both (read-before-write *and* auto-create parent directories).

## 4. Permission, sandbox & safety

File write is universally treated as a mutating, approval-worthy operation — it never appears in the read-free tier of any permission model observed. Common patterns:

- **Per-call prompt with persistent rules**: write/edit tools require explicit approval on first use, with a save-to-settings option to avoid repeat prompts. Some harnesses explicitly separate "read-only operations... require no approval" from "write/execute operations," which do.
- **Category-based auto-approval**: a standalone "edit files" toggle governs the write tool independent of shell/browser/MCP toggles.
- **Hardcoded overrides regardless of trust config**: even with a broad allow-all trust setting, writes to sensitive paths (a `.git/` directory, an agent's own hook/permission configuration) always prompt — an explicit carve-out preventing the agent from silently rewriting its own permission or hook configuration.
- **Staged apply gate**: the write never touches disk until the user runs an explicit apply step, making that step the single, unavoidable permission gate for every file write in the session.
- **Configuration-only, user-driven**: files must be explicitly added to the working set before the model can write them at all; there is no autonomous write outside that scope.

Sandboxing rarely targets file write specifically — OS-level sandboxes restrict *where* writes can land (working directory, worktree root) rather than gating the write operation itself, and several harnesses explicitly exclude their built-in write tool from the sandbox boundary — using the permission system directly instead. That means for this capability the permission layer, not the sandbox, is doing the real safety work in most implementations. Harnesses with no sandbox at all for file operations rely entirely on the permission layer plus, in some cases, an ignore-file convention to keep writes out of sensitive paths.

The risk profile of file_write is qualitatively different from file_read or search: an unreviewed write can silently destroy existing content (no OS trash/undo unless the harness maintains its own checkpoint state, and even then a revert is itself typically irreversible). This is the concrete justification for pairing file_write with either a read-before-write invariant, an approval prompt, or a staged-apply review — three different mechanisms converging on the same goal of preventing silent data loss.

## 5. Convergent patterns & divergences

**Convergent**: near-universal presence, full create-or-overwrite semantics as a single operation, no append/merge capability anywhere, and universal treatment as a mutating/approval-gated operation rather than a free action. Read-before-write, where present, is a harness invariant rather than a policy toggle — it can't be disabled by an approval setting, only satisfied by an actual prior read.

**Divergent**: the exposure mechanism (native function call vs. freeform grammar vs. prompt-injected text/XML) splits along similar lines to file_edit generally, and for this capability specifically correlates with target model family. The degree of gating also diverges sharply: from a fully manual "add file, then request write" flow, to hardcoded always-prompt paths regardless of trust settings, to apply-time-only materialization, to (at the permissive end) default-autonomous execution with no required approval for any tool call including file creation.

**Trend**: an implementation with no dedicated write primitive — folding file creation into the edit tool or leaving it to shell — is the outlier worth noting; every other implementation treats "create a brand-new file" as important enough to warrant its own named operation, distinct from "modify part of an existing file."

## 6. Implications for PluggableHarness Agent

`file_write` maps directly to the reference catalog's `filesystem`/`write_file` row: `kind = resource`, `risk = moderate`, described as create-or-overwrite (see [the reference catalog](../../specifications/tool/reference-catalog.md)). This is uncomplicated: every implementation treats file_write as a mutating, gated operation (never `data_source`), which confirms `resource` is correct, and no implementation treats file writes as intrinsically high/critical risk the way shell execution or subagent spawning are — consistent with `moderate` rather than `high`.

One point worth flagging as corroborating design context, not a spec amendment: the read-before-write contract found across several independent implementations is a harness-level invariant, not a protocol concept — the tool provider's `GetSchema`/`Invoke` surface has no dedicated field for "the target path must have been read this session." If a first-party `write_file` reference provider wants to reproduce this widely-converged safety property, it would need to be implemented as provider-internal state (tracking read paths) or as an `agent.hcl` policy precondition, rather than as a protocol-level schema field.

This capability does not bear on the reference catalog's two explicitly-called-out ambiguous classification calls (`bash`/`exec` and `web_fetch`) or on the `interactive` kind discussion — `write_file`'s classification is uncontested.
