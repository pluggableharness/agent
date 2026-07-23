# Notebook Edit

## What it is

Notebook editing is the capability to read, modify, and (in the richest implementations) execute cells of a Jupyter notebook (`.ipynb`) as a structured, cell-addressable document rather than as an opaque text blob. A `.ipynb` file is JSON internally — a list of cells, each with a type (code/markdown), source text, and (for code cells) stored outputs and execution counts. Treating it as plain text for edits is workable but loses the structure: a harness that only has generic file-edit can still open and text-patch the underlying JSON, but doing so correctly requires JSON-aware escaping and risks corrupting cell metadata or output state that a naive string-replace wouldn't preserve.

The capability spans two rungs of sophistication: (1) *structural editing* — inserting, replacing, or deleting cells by identity (e.g. a `cell_id`) instead of by string match, and (2) *execution and inspection* — actually running a cell and retrieving its output, which requires a live kernel connection, not just file mutation. Reaching rung two is rare.

In a coding agent's workflow, this sits alongside file-edit and file-read as a specialization for one file format, not a distinct workflow phase — it exists because notebooks are common in data-science and ML repositories where a plain-text diff view of the underlying JSON is unreadable and unsafe to hand-edit via a generic `old_str → new_str` tool.

## Common implementation patterns

Coverage of this capability is thin, and the design space is really a split between two points rather than a spectrum:

- **Cell-addressable edit only**: a dedicated tool operates on a specific `cell_id` with an explicit mode (replace/insert/delete). This is structurally closer to file-edit's "targeted replacement" family than to a general text-patch tool — it just addresses cells instead of line ranges or search strings, and does not perform string replacement across the notebook the way a generic edit tool would.
- **Execution-capable cluster**: rather than one tool, a cluster of tools handles create, summarize, edit, execute, and read-output separately. This lets the model actually run a cell and retrieve its result within the notebook-editing capability itself (as opposed to running notebook code via a generic shell/`jupyter nbconvert` invocation) — commonly built on top of an IDE's native Jupyter execution infrastructure rather than the harness shelling out to a kernel itself.
- **Read-only extraction, no dedicated edit tool**: a generic read tool transparently extracts `.ipynb` text alongside other binary formats (`.docx`, `.xlsx`), giving the model visibility into notebook content without any purpose-built edit affordance. Edits, if they happen, fall through to whatever generic edit/patch mechanism the harness otherwise has, with no documented JSON-structure awareness.

The two implementations that go further than read-only extraction agree on one architectural point: treating `.ipynb` as opaque text is inadequate, so both build cell-level addressing rather than exposing raw JSON to a generic text-edit tool. Where they diverge is scope — one scopes the capability narrowly to *editing* cells (reading handled by a pre-existing generic read tool that already handles images/PDFs/plain text), the other scopes it broadly to the full notebook lifecycle as several separate tools.

## Permission, sandbox & safety

A dedicated `NotebookEdit`-style tool commonly requires the same permission as the harness's generic file-edit tool — folded into the same write/execute approval tier rather than classified as a separate operation. No sandboxing distinction is typically documented for notebook edits specifically; built-in file tools generally sit outside OS-level sandboxing and rely on the permission system directly.

An execution-capable cluster inherits the IDE agent-mode permission model of its host: the execute-type operation (running a cell) is closer in risk profile to running a shell command than to a text edit, since it runs arbitrary notebook code against a live kernel, while the edit-type operation sits with the harness's other file-edit tools shown as a diff for review before applying. A distinct approval prompt specifically for cell execution versus other execute-family tools is not always documented — it commonly falls under the same per-invocation execute approval used generically for other run-code tools.

The risk profile of this capability is bimodal depending on which rung is implemented: pure structural cell edit is comparable in risk to file-edit — a mutation confined to one file, recoverable via version control. Cell *execution* is materially riskier: it runs arbitrary code against a live kernel process, closer to `bash`/`exec`'s risk profile than to `edit_file`'s. Read-path extraction carries no execute risk at all — it's a data_source-shaped operation reading and rendering notebook content, with no permission gate distinct from a general read-only tool's status.

## Design considerations

With only a handful of implementations showing any signal, there isn't a large sample to declare a strong convergent pattern. Notebook editing remains rare and largely proprietary — this looks less like an emerging convergence and more like a small number of harnesses solving a niche (data-science/ML repo) need independently, without enough adoption pressure yet to produce a shared naming or mechanism convention the way file-edit or MCP client support have.

## Implications for PluggableHarness Agent

The [tool reference catalog](../../specifications/tool/reference-catalog.md) builds strictly from common-core capabilities; notebook editing falls well under that threshold and is not listed among its reference rows (`filesystem`, `search`, `exec`, `web`, `task`, `agent`, `user`) — none of which notebook editing maps onto directly, since it's neither generic file read/write/edit (the format-specific JSON-cell structure is the whole point) nor a new category the protocol anticipates. This is a clean case for the catalog's own inclusion criterion: a differentiator, not a gap. A third-party `notebook` tool provider (e.g. `resource` kind for cell edit/create at `moderate` risk, mirroring `edit_file`'s classification rationale, plus optionally a separate execute-type operation at `high` risk mirroring `bash` if a provider chooses to support live kernel execution) is fully expressible under the existing plugin protocol without any spec change — the reference catalog doesn't need a dedicated notebook row to accommodate one.

One point worth flagging for whoever eventually writes such a provider: if a third-party provider does implement cell execution, the [ambiguous classification](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls) reasoning for `bash`/`exec` — that a single operation spanning read-only and destructive invocations should default to the gated/`resource` classification rather than attempting content-based reclassification — applies with at least as much force to "execute a notebook cell" as it does to shell commands, since notebook code is equally unconstrained. This doesn't bear on any current open question in the tool protocol's conformance notes; it's a design note for a provider author, not a protocol gap.
