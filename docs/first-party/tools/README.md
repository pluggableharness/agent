# Tool capability reports — index

This directory contains first-party reference documentation for tool-provider capabilities — the operations a coding agent's tools commonly implement (file I/O, shell execution, search, web access, task tracking, sub-agent spawning, and similar).

> [!IMPORTANT]
> These are descriptive reference reports, not the protocol spec itself: the tool-provider protocol, its data types, and the reference catalog's `kind`/`risk` classifications live in [`docs/specifications/tool/`](../../specifications/tool/README.md), which remains the source of truth for this project's own design. Each report links to the corresponding part of that protocol where relevant.

## Reports

| Capability | Report | Corresponding spec |
|---|---|---|
| File read | [file-read.md](file-read.md) | `filesystem` / `read_file` |
| File write | [file-write.md](file-write.md) | `filesystem` / `write_file` |
| File edit | [file-edit.md](file-edit.md) | `filesystem` / `edit_file` |
| Multi-file edit / apply_patch | [multi-file-edit.md](multi-file-edit.md) | Differentiator — informs the `edit_file` mechanism choice |
| Glob / file search | [glob-file-search.md](glob-file-search.md) | `search` / `glob` |
| Grep / content search | [grep-content-search.md](grep-content-search.md) | `search` / `grep` |
| Shell / bash exec | [shell-exec.md](shell-exec.md) | `exec` / `bash` |
| Web search | [web-search.md](web-search.md) | `web` / `web_search` |
| Web fetch | [web-fetch.md](web-fetch.md) | `web` / `web_fetch` |
| Subagent / task spawn | [subagent.md](subagent.md) | `agent` / `spawn_subagent` |
| Todo / task tracking | [task-tracking.md](task-tracking.md) | `task` / `task_create`, `task_update`, `task_list` |
| Notebook edit | [notebook-edit.md](notebook-edit.md) | Differentiator — third-party `notebook` provider |
| MCP client | [mcp-client.md](mcp-client.md) | Not a tool operation — a tool-sourcing mechanism |
| LSP / code intelligence | [lsp-code-intelligence.md](lsp-code-intelligence.md) | Differentiator — not in the reference catalog |
| Browser automation | [browser-automation.md](browser-automation.md) | Differentiator — deferred to third-party providers |
| Git / VCS | [git-vcs.md](git-vcs.md) | Not in the reference catalog — mostly folded into `bash` |
| Memory / persistence | [memory-persistence.md](memory-persistence.md) | Excluded as redundant with the memory provider category |
| Plan mode | [plan-mode.md](plan-mode.md) | Plan/apply gate — not a tool operation |
| Diagnostics / lint | [diagnostics-lint.md](diagnostics-lint.md) | Differentiator — not in the reference catalog |
| Image / vision input | [image-vision.md](image-vision.md) | Model capability (provider protocol) — not a tool operation |
| User question / elicitation | [user-question.md](user-question.md) | `user` / `ask_user` (`kind = interactive`) |
| Cron / scheduling | [cron-scheduling.md](cron-scheduling.md) | Differentiator — deferred to third-party providers |
