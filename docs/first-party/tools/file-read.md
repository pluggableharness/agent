# File Read

## 1. What it is

File read is the operation that returns the contents of a file at a given path to the model's context: the single most basic grounding action a coding agent takes before it can reason about, edit, or discuss code. In nearly every harness it is the first tool called in a session and the highest-volume tool call type overall, since edit/write operations in most implementations require (or are made safer by) a prior read of the same file.

Functionally it spans a narrow core — read a file (or a byte/line range of one) and return text — and a wider periphery that harnesses graft onto the same tool: directory listing, binary-format text extraction (DOCX, XLSX, Jupyter notebooks), image/vision content for multimodal models, and even reading non-file content (an open IDE editor buffer, an MCP resource, a git diff, a background task's output). Where a harness draws that boundary — one do-everything `read` tool versus several narrowly-scoped ones — is itself one of the more informative points of cross-tool variation (§3).

In a coding agent's workflow, file read sits upstream of nearly everything else: search tools (glob/grep) locate candidate files, file read confirms their contents, and edit/write tools then act on state the model has actually seen. Several harnesses formalize this ordering as a read-before-write contract (§3), turning file read from a convenience into a correctness precondition for the write path.

## 2. Adoption and mechanism

File read is effectively universal across coding harnesses — no harness's file-read capability is genuinely absent; where a dedicated tool isn't named, reads still happen implicitly (e.g. via a patch tool's context lines, or by shelling out to `cat`).

Naming converges loosely on `read`/`read_file`/`Read` (or trivial variants like `fs_read`, `view_file`, plural `read_files`) — one of the few capabilities with little naming disagreement, unlike the free-for-all around edit mechanisms. The structural outliers model reading as a sub-command or side effect of a different tool family rather than a standalone verb — e.g. an editor tool's `view` sub-command, or `open`/`scroll_up`/`scroll_down` cursor-navigation primitives.

One harness in this space has no read tool call at all: files are read by injecting their full text into the prompt once explicitly added to the chat by the user, rather than exposing an invocable read action to the model. The model can only reference a repo map and hope the user adds a file, or ask the user to. This is consistent with a broader prompt-injection architecture rather than being specific to reading.

## 3. Cross-tool variation

**Call signature and paging.** The dominant pattern is offset/limit or start/end line-range parameters over a flat file path, sometimes batched as an array of `{path, start_line, end_line}` entries in one call. Some implementations cap ranges at a few hundred lines per call and add a "summarize the rest" flag so the model can request a summary of content outside the requested window rather than paying for a second full read. A structurally different pattern models reading as **stateful cursor navigation** — jump to a line, then scroll a fixed window up/down — rather than the stateless range-request model most tools use.

**Content types beyond plain text.** Some implementations fold binary-format extraction into the same tool: images (downscaled to fit model limits) and paginated PDFs alongside plain text and Jupyter notebooks, or DOCX/XLSX/ODS extraction transparently in the same read path. Others split this differently — a dedicated image-reading tool, separate from whatever handles plain-text reads. This is a real design fork: one tool that branches on file type internally, versus several tools scoped to content type.

**Reading things that aren't files.** Several implementations extend "read" past the filesystem: reading the IDE's active editor buffer with no path argument at all; reading a named MCP resource; reading a past session transcript; reading the working git diff; reading a background task's output. Every one of these is architecturally "read something and put it in context," reusing the same low-risk classification as file read even though the target isn't a file on disk.

**Read-before-write / read-before-overwrite contracts.** This is the most consequential semantic variation, since it turns file read from a pure convenience into a load-bearing precondition elsewhere in the toolset. Several implementations' write tool fails if overwriting a file that hasn't been read in the same conversation, and their edit tool fails if the file changed on disk since the read, or requires a prior read with a uniquely-matching old-text argument. None of these enforce it on the *read* tool itself — the constraint lives on the write/edit side, but it makes file read a protocol-relevant dependency rather than an inert lookup.

## 4. Permission, sandbox & safety

File read is the canonical `read_only` operation and is treated as such almost everywhere: plain reads sit in the "no approval needed" tier of every tiered/category-based permission scheme observed — grouped alongside glob/grep as auto-approved, or as an independently-toggleable "read files" category that can be freely enabled while edits/execution stay gated.

The documented exceptions are informative because they gate on *location* rather than *operation type*: some implementations trust reads by default only within the current working directory, requiring approval for reads of paths outside it — the same boundary some harnesses enforce for a sandboxed terminal tool (project worktree root) or an OS-level sandbox's filesystem default. One notable exception gates specifically on reading the IDE's *currently open* buffer, seemingly because it can surface content the user hasn't explicitly referenced in the conversation.

Sandboxing is largely orthogonal to file read specifically — OS-level sandboxes are aimed at the shell/exec tool, and several harnesses explicitly exclude built-in file tools from the sandbox boundary, using the permission system directly instead. The practical risk model for file read is not process isolation but **secrets and information exposure**: reading `.env`, credentials, or other sensitive files puts their contents in the model's context, which several harnesses address through filesystem-boundary ignore-file conventions rather than through approval gating on the read call itself. Some implementations go further and redact environment-variable-shaped secrets (matching patterns like `TOKEN`, `PASSWORD`, `KEY`) before exposing them to the model — a content-based mitigation orthogonal to path-based ignore files.

## 5. Convergent patterns & divergences

**Convergent**: file read is read-only, requires no approval by default, and is about as close to universal, uncontested agreement as any capability in this space gets. Line-numbered, paginated output for large files is a near-universal convention too — a UX/token-economy choice rather than a protocol necessity.

**Where implementations split**: (1) whether "read" is one do-everything tool that branches internally on file type, or several type-scoped tools; (2) whether reading is a discrete request/response call (the majority) or stateful cursor navigation; (3) whether the model can invoke a read at all — in at least one architecture, reading is entirely a user-mediated, prompt-injection side effect of an explicit add command, not a model-callable action.

**Observed trend**: the periphery is expanding faster than the core. The core "read a text file" primitive hasn't meaningfully changed across implementations; what's grown is what else gets routed through the same tool or a sibling with the same low-risk classification — binary document formats, MCP resources, session transcripts, IDE buffer state, git diffs. Harnesses are consolidating read-shaped capabilities under one low-friction, no-approval umbrella rather than minting new approval-gated tools for each.

## 6. Implications for PluggableHarness Agent

File read maps directly to the reference catalog's `filesystem`/`read_file` row: `kind = data_source`, `risk = read_only`, with line-range targeting and line-numbered output as the reference convention (see [the reference catalog](../../specifications/tool/reference-catalog.md)). This is about as strong a case for `data_source`/`read_only` as any capability gets — uniform read-only treatment across essentially every permission model observed, and the line-range/line-numbered-output convention is exactly the dominant call-signature pattern in practice.

Two points bear on choices the reference catalog doesn't yet make explicit, though neither rises to a spec defect:

- **Scope of the reference `read_file`.** Real-world implementations vary in how much lives inside "read" versus a sibling tool (image/PDF/notebook extraction folded in for some, split out as a dedicated image-read tool for others). The reference catalog doesn't specify whether PluggableHarness Agent's reference `filesystem`/`read_file` should branch on content type internally or stay narrowly scoped to text, deferring richer content (images, PDFs) to the model provider's vision/attachment path instead. Given PluggableHarness Agent's plugin-per-concern philosophy and that the model-provider category already owns vision as a model capability, a narrowly-scoped text-only `read_file` with binary-format extraction left to third-party tool providers seems the better fit than replicating a do-everything tool — but this is a recommendation, not something the current spec text mandates either way.
- **Read-before-write coupling.** The read-before-write/read-before-overwrite contract (§3) is a cross-tool invariant spanning `read_file` and `edit_file`/`write_file` that the reference catalog doesn't currently call out explicitly for its own reference rows. Since several independent, widely-used implementations converge on it, it's worth PluggableHarness Agent's reference `filesystem` provider considering the same invariant — but enforcing it is a provider-internal state-tracking concern (has this path been read in this session, has it changed since), not something that needs new protocol surface in `GetSchema`/`Invoke`.

File read's near-total consensus as a free-running, read-only, no-approval operation makes it one of the least contested capabilities in this reference material.
