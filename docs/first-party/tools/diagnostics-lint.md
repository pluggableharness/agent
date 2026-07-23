# Diagnostics / Lint Integration

## 1. What it is

Diagnostics/lint integration is the capability that surfaces structured, tool-generated correctness signals — compiler errors, linter warnings, type errors, test failures — back into the model's context without the model having to run and parse an ad-hoc shell command itself. It sits downstream of an edit: a harness makes a change, then either the model or the harness itself calls a diagnostics operation to check whether the change introduced a problem, closing an edit → verify → fix loop within a single turn or across a couple of turns.

The capability spans two distinct mechanisms. Some implementations expose it as a **query tool** the model calls on demand — "give me the current errors for this file/project" — sourced from an already-running language server or IDE Problems panel. Others fold it into the **edit path itself** — the harness runs a linter as a side effect of an edit or a command and force-feeds the output back into the transcript, either automatically or as a blocking precondition on the edit succeeding. A third variant treats it as a narrower structural-analysis tool rather than a live compiler/linter integration.

Diagnostics/lint is closely related to, but distinct from, LSP/code intelligence (see [`lsp-code-intelligence.md`](lsp-code-intelligence.md)) — several implementations fold diagnostics into a broader LSP-backed tool that also does go-to-definition, find-references, and rename, while others treat lint/diagnostics output as compiler/linter text with no LSP involvement at all. This document focuses on the diagnostics/lint signal specifically and calls out the LSP overlap where relevant.

Where it sits in the workflow: after `edit_file`/`write_file`, before the harness or model decides the change is complete — a verification step analogous to (and sometimes literally implemented via) running the test suite, but scoped to static analysis rather than execution.

## 2. Adoption and mechanism

This is a moderately-adopted, differentiator-tier capability, not part of the universal core. Harnesses without a dedicated diagnostics operation generally rely on the model invoking `bash`/shell directly to run a linter or type-checker and reading raw stdout — an ordinary shell-execution pattern, not a dedicated diagnostics operation.

| Pattern | Example mechanism |
|---|---|
| Standalone query tool, called by the model on demand | Reads from an already-running source of truth — an IDE's Problems panel, a live LSP session, or a compiler/linter process — rather than invoking a fresh lint run per call. |
| Harness-triggered auto-feedback, not model-callable at all | Runs a linter after every edit round without the model asking for it, injecting errors into context so the model can self-correct on the next turn — the harness enforcing a check the model doesn't control. |
| Edit-time gate | Runs the linter as a precondition on the edit tool itself — a syntactically invalid edit is rejected before it's ever applied, rather than accepted and then separately flagged. Stricter than the other two patterns: the model never sees a broken file state to react to, because the edit simply fails. |

## 3. Cross-tool variation

**Scope granularity** differs: some implementations support both single-file and whole-project scope; others are scoped to whatever an editor's Problems panel currently holds (itself IDE-dependent); others take a file-or-directory argument, or can target in-chat files vs. all dirty files. None of the observed implementations expose a single-symbol diagnostics scope (contrast with LSP find-references, which is inherently symbol-scoped).

**Source of the underlying signal** varies: some implementations source diagnostics from an actual Language Server Protocol session — meaning they inherit whatever a real LSP server reports (type errors, unresolved imports, etc.), not just syntax. Others are one layer further removed, reading whatever an IDE's own Problems panel has already aggregated. Still others invoke language-specific linters/syntax checkers directly with no LSP layer involved at all — a simpler but narrower signal.

**Naming has no convergence** — `get_errors`, `diagnostics`, `get_diagnostics`, and `/lint`-style commands are different names for closely related operations, unlike `read_file`/`grep` which have mostly converged on shared vocabulary.

## 4. Permission, sandbox & safety

Diagnostics/lint is uniformly low-risk — it is a read of derived, non-authoritative state (compiler/linter output) or, at most (the edit-gate variant), a rejection of an edit that would otherwise have been a normal `resource`-risk operation. No implementation observed describes an approval gate specific to diagnostics — it inherits whatever the harness's default read-only treatment is, whether that's a read-only tool tier, an auto-approved category, or (for the harness-triggered and edit-gate patterns) no runtime approval step at all since there's no separate call to gate.

No sandboxing is typically described for diagnostics/lint specifically — it executes in-process (reading an LSP session's cached state) or as a lightweight, short-lived linter subprocess, rather than the kind of full shell-execution surface that motivates OS-level sandboxing for the `bash`/`exec` capability. The risk profile here is closer to `web_fetch`'s "read, but be honest that it's not free of side effects" caveat than to `bash`'s — except diagnostics/lint has no analogous side-effect concern at all (it doesn't hit a network endpoint or mutate anything), making it lower-risk than either.

## 5. Convergent patterns & divergences

**Convergent**: every implementation treats this as a low-risk, read-style operation with no distinguished approval gate.

**Divergent**: the field splits on *when* the check happens — model-invoked query, harness-auto-triggered after every edit, or edit-time hard gate — and on *where the signal comes from* — a live LSP session, an IDE's own aggregated Problems panel, or a direct linter/syntax-checker subprocess with no LSP involvement. There is no dominant pattern the way `old_str → new_str` is the plurality choice for file editing.

The clearest overlap worth flagging: the implementations with the *strongest* diagnostics support are exactly the ones that also have a real LSP/code-intelligence tool — diagnostics in those cases is not a standalone capability but one operation of a broader LSP-backed tool. Implementations with diagnostics but no LSP tool source the signal from something else (an IDE panel, a direct linter invocation) instead.

## 6. Implications for PluggableHarness Agent

Diagnostics/lint is not part of the first-party tool reference catalog — it sits in the differentiator tier alongside browser automation and memory-as-a-tool, left to future or third-party providers rather than shipped as a first-party reference tool (see [the reference catalog](../../specifications/tool/reference-catalog.md)).

If PluggableHarness Agent does eventually add a diagnostics/lint reference provider, two shapes look viable, mirroring the `kind`/`risk` classification style already used for the reference catalog's other rows:

- As a **`data_source`, `read_only`** operation (e.g. a new `lsp` provider category exposing `get_diagnostics`) — the dominant pattern across query-tool implementations, and consistent with the permission analysis above (no approval gate observed anywhere). This is the natural fit given the protocol's `kind` semantics: it reads derived state and has no external blast radius.
- Bundled into a broader **LSP/code-intelligence provider** rather than standing alone — several implementations already fold diagnostics into their LSP tool rather than exposing it separately. Since LSP/code-intelligence itself is also absent from the current reference catalog (also a differentiator), a future spec revision addressing LSP integration would be the natural place to fold diagnostics in as one of several bundled operations (definition/references/diagnostics/rename), rather than standardizing it as an independent operation.

Either shape classifies cleanly under the existing `resource`/`data_source`/`interactive` `kind` enum with no new classification machinery needed — unlike `bash`/`exec` or `web_fetch`, diagnostics/lint raises no ambiguous classification questions.
