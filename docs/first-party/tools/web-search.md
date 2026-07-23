# Web Search

## What it is

Web search is a tool operation that takes a natural-language or keyword query and returns a list of ranked results — typically titles, URLs, and short snippets/summaries — from a live or cached search index. It is distinct from web fetch/URL-read (which retrieves the full content of a *known* URL): search discovers URLs the model doesn't already have, while fetch retrieves what's already been found. In a coding agent's workflow, search is invoked when the model needs current information outside its training data or the local repository — library version changes, error message lookups, current API documentation, or general facts — and results usually feed into a follow-up fetch of one or more of the returned URLs rather than being consumed directly.

Because it depends on an external, usually vendor- or provider-hosted, search backend rather than local filesystem or process state, it sits architecturally closer to a thin API-wrapper tool than to a harness's core file/shell primitives. Some designs don't implement search themselves at all, instead delegating to whatever native web-search capability the underlying model vendor already exposes.

## Design considerations

**Own hosted backend vs. third-party API wrapper vs. delegated to the model vendor** are the three broad architectural choices, and they determine who bears the cost/rate-limit/quality tradeoffs of the search backend — the harness itself, a third-party search API, or the model vendor. Delegating to the model vendor's own search tool ties the capability's availability to model choice, which is a real coupling worth being explicit about.

**Domain allow/deny scoping is a common configurability point**, even though the exact parameter shape (an optional filter list vs. regex trusted/blocked patterns) varies.

**Availability as its own axis of variation.** Unlike core file/shell tools, which are essentially always present once a harness ships them, search's dependence on a paid/hosted external backend means it's commonly gated by deployment target, subscription tier, or provider selection — an availability question distinct from the tool's approval status.

**Search results feeding a follow-up fetch carry the same indirect-injection risk fetch itself carries** (poisoned search-result content reaching the model's context) — see `web-fetch.md`.

## Permission, sandbox & risk classification

`web_search` is classified `kind = data_source`, `risk = read_only` — it has no widely-observed "search with side effects" variant the way `bash` or `web_fetch` do, so there's no ambiguous-classification call to make here. See [`reference-catalog.md`](../../specifications/tool/reference-catalog.md) for the full reference-catalog entry.

Despite being `read_only`, requiring per-call approval for search is still a defensible policy choice — network egress and indirect prompt-injection via poisoned results are policy-level concerns distinct from the `kind`/`risk` classification itself, which only determines whether the plan/apply gate applies at all, not how tightly an operator chooses to gate it further. No OS-level sandbox typically isolates web search specifically; where a sandbox exists, it usually governs shell/file access, and search either goes through a harness-controlled hosted backend or a model-vendor-side call outside the harness's own sandbox scope. `kind = interactive` does not apply here — search is a straightforward machine-to-machine call with no turn-blocking human input.
