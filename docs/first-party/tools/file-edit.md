# File Edit

## 1. What it is

File edit is the operation that applies a **targeted, partial modification** to an existing file, as distinct from `file_write` (create-or-overwrite the whole file) and `file_read` (no mutation at all). It is the mechanism a coding agent uses for the overwhelming majority of real work — fixing a function, renaming a symbol, adjusting a config value — where rewriting the entire file would be wasteful of both tokens and correctness (a full rewrite risks the model silently dropping unrelated lines it didn't mean to touch).

Concretely, "file edit" covers any tool whose contract is "locate a specific span of an existing file (by string match, line range, or diff hunk) and replace it," as opposed to "here is the file's new complete contents." This is a distinct concern from `file_write` and from coarser multi-file/multi-hunk atomic patch operations, though in practice these lines blur — a single patch-envelope tool, for instance, can edit one file or many in one call.

Within the coding-agent workflow, file edit sits at the center of the read-observe-edit-verify loop: the model reads a file (or has it in context from a prior turn), decides on a change, emits an edit call, and — in harnesses with read-before-write discipline — the harness rejects the edit if the file was read stale or the target text isn't uniquely identifiable. This makes file edit one of the highest-friction points for correctness bugs (a wrong or ambiguous match silently touching the wrong location) and one of the most heavily instrumented points for permission gating, since it is the harness's primary channel for mutating the user's actual source tree.

## 2. Adoption and mechanism

File edit is effectively universal — every coding agent needs some way to make a targeted change to an existing file, and the field agrees this needs to be a distinct capability from whole-file overwrite. At least six distinct edit mechanisms are in active use:

1. **`old_str → new_str` exact-match replacement** — the plurality pattern. The "old" text must appear in the file, and most variants of this pattern require it to be *uniquely* identifiable — an ambiguous match is rejected rather than guessed at.
2. **Code-edit with placeholder syntax** — an ellipsis-style marker (e.g. `// ... existing code ...` or `{{ ... }}`) denotes "leave this part of the file unchanged," letting the model emit a sparse diff-like body without full search/replace ceremony.
3. **Whole-file rewrite** — the model returns the complete new file content and the harness overwrites. Cheapest to implement, most token-expensive, and a format some harnesses adopted specifically to avoid "lazy" partial-file responses from weaker models.
4. **Search/replace block text format** — using `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE`-style markers parsed out of fenced code blocks in the model's plain-text reply, not a tool call at all.
5. **A multiplexed editor tool with sub-commands** — view/create/replace/insert/undo exposed as one tool, modeled on Anthropic's computer-use tool lineage.
6. **A patch-envelope format** — a `*** Begin Patch ... *** End Patch` grammar shared across several unrelated codebases, gated to a specific model family in most of them — this looks less like independent convergence and more like several harness authors converging on whatever a given model vendor's own models were fine-tuned or documented to produce well, then generalizing that into a reusable freeform-grammar tool definition.

**Model-gated routing** is a striking cross-cutting pattern: several implementations independently route "pick the native edit tool for one model family, pick the patch-envelope tool for another" as a runtime decision keyed on model ID or preset, not a static per-harness choice.

## 3. Cross-tool variation

**Read-before-edit discipline** varies: several implementations explicitly fail if the file was not read in the same conversation or has changed on disk since it was read, and require an exact, unique match; others require an exact, unique match without a stated read-first requirement. One notable orthogonal constraint some implementations add: an edit tool that cannot be called in parallel with any other tool call, including another instance of itself — a documented concurrency limitation rather than a data-integrity check.

**Batching / multi-edit** is a secondary differentiator layered onto the base mechanism: a flag that turns a single-match edit into an all-occurrences edit; a dedicated multi-edit tool that batches multiple find/replace operations into one call; and YAML-selected windowed-edit bundles offering several distinct edit granularities (single-match replace, line-range replace with linting, full-window rewrite) as alternatives to a default editor.

**Undo** is rare but present in some implementations: a dedicated undo operation reverts the most recent edit and returns a diff of what was undone, sometimes bundled as a sub-command of a multiplexed editor tool. Everywhere else, version control via git is the implicit undo mechanism.

**Linting-gated edits**: a runtime linter check before accepting an edit, blocking syntactically invalid changes — a correctness gate built into the edit call itself rather than as a separate downstream tool.

## 4. Permission, sandbox & safety

File edit is near-universally treated as a **mutating, gated** operation — it falls squarely into the harder-gated half of every harness's permission tiering, in contrast to read-only tools which mostly execute freely.

Common approval patterns:

- **Per-call prompt with persistent rules**: explicit approval required on first use per project, with a scoped "don't ask again" allow rule, or a general ask/allow/deny-per-tool-name system defaulting to "ask."
- **Category-based auto-approval**: a distinct "edit files" toggle, independent of read/execute/browser/MCP toggles — an operator can auto-approve edits while still gating shell commands.
- **Config-only, no runtime prompt**: once a file is explicitly added to the working set, subsequent edits apply without a further per-edit confirmation; the gate is the one-time, explicit file add, not a per-call approval.
- **Staged apply gate**: all file edits (along with move/remove/reset operations) are deferred to an explicit apply step — edits accumulate as a plan diff and never touch disk until the user reviews and applies, the most conservative model for file edit specifically.
- **No gate at all**: some implementations let file writes proceed without approval except for sensitive config files — a genuine divergence from the rest of the field, which treats edit approval as the default posture for write-class tools.

Sandboxing shows a **consistent gap specific to file edit**: OS-level sandboxes are aimed at the shell/exec tool, and several harnesses explicitly exclude their built-in file tools (read/edit/write) from the sandbox boundary — they use the permission system directly instead. File edit's safety net across the field is the permission-prompt layer, not process or filesystem isolation — a meaningfully different risk model than shell exec, where a sandbox can contain a runaway command regardless of what the approval layer allowed.

The risk this capability carries is narrower and more predictable than shell exec — an edit's blast radius is bounded to one (or, with multi-edit, a handful of) named file(s), versus shell's unbounded command surface — which plausibly explains why some implementations treat it with looser gating than shell commands even while keeping shell tightly permissioned.

## 5. Convergent patterns & divergences

**Convergent**: every implementation needs some way to make a targeted change to an existing file, and treats this as needing to be distinct from whole-file overwrite. There's also broad (if not universal) agreement that an edit call should be rejected on an ambiguous or non-unique match rather than silently picking one occurrence.

**Where it splits**: naming and encoding. Unlike, say, `file_read`, where naming varies but the underlying contract barely does, file-edit implementations genuinely disagree on the *shape* of an edit — string match-and-replace vs. placeholder-annotated diff vs. line-range rewrite vs. whole-file vs. patch envelope vs. prose-parsed search/replace. This is likely the most differentiated common-core capability precisely because it sits at the intersection of two hard tradeoffs the field hasn't converged on: token efficiency (a targeted diff is cheaper than a whole file) versus model reliability (models are more error-prone at producing exact substring matches or line numbers than at producing whole-file output, especially weaker models).

**Observed trend**: model-vendor-specific patch grammars are spreading independently of harness lineage, gated specifically to one model family in most implementations that use them — this looks less like harness convergence and more like harnesses converging on whatever one vendor's models were fine-tuned or documented to produce well.

## 6. Implications for PluggableHarness Agent

File edit maps directly onto the reference catalog's `filesystem`/`edit_file` row: `kind = resource`, `risk = moderate`, with the `old_str → new_str` exact-match-replacement pattern as the reference implementation — it's the plurality choice among discrete-tool mechanisms and needs no grammar-constrained parsing, unlike patch-envelope-style approaches (see [the reference catalog](../../specifications/tool/reference-catalog.md)). The grammar-constrained alternative is disproportionately tied to one model family rather than being an independently-arrived-at design choice, a reasonable basis for treating it as not the reference pattern. `file_edit`'s `resource`/`moderate` classification is settled: no implementation treats file edit as read-only, and none classifies it above shell exec in risk.

Two points worth flagging as design considerations, not settled protocol gaps:

- **Read-before-edit as a precondition.** Several independent implementations enforce "the target file must have been read in this session, and the match must be unique" as a hard precondition before an edit executes. The protocol's error taxonomy could express this (`invalid_arguments` for an ambiguous/non-unique match, `not_found` for content that doesn't exist in the file), but doesn't currently call out staleness detection ("fails if the file changed since read") as an expected `edit_file` behavior. Given how many independent implementations converge on it, it's worth considering for a future refinement of the reference catalog's `edit_file` notes — though it isn't a protocol-level gap since a provider is free to implement it under the existing schema.
- **Per-path concurrency locking is a case PluggableHarness Agent formalizes ahead of the field.** The `ConcurrencySpec` example (`safe: true, key_fields: ["path"]` for filesystem edit/write) is exactly the semantics implementations achieve only *implicitly*, via single-model, effectively-serial tool-call execution — no implementation observed documents an explicit concurrency contract for concurrent edits to different files, because none of them appear to actually parallelize model-initiated edit calls within a turn today. PluggableHarness Agent's `ConcurrencySpec` is a genuine protocol-level advance here, not something existing practice validates — worth flagging as an area where PluggableHarness Agent's design outruns current practice rather than distilling it.

Edit-level undo is one edit-adjacent capability with no current home in the reference catalog. It's a plausible candidate for a differentiator tool provider (a narrow `filesystem`/`undo_edit` operation) rather than a reference-catalog addition, given how few implementations support it — consistent with the catalog's stated policy of reserving the reference set for common-core capabilities and leaving thinner-coverage capabilities to third-party providers.
