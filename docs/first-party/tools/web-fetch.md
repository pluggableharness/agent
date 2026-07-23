# Web Fetch

## What it is

Web fetch is the operation of retrieving the content of a single, caller-specified URL and returning it to the model as usable context — typically converted to Markdown or plain text, sometimes chunked for long documents. It is distinct from web search (query in, ranked result titles/URLs out, no page content) even though the two are frequently paired in a workflow: a search tool locates candidate URLs, and a fetch tool retrieves the content of one the model or user selected.

In a coding agent's workflow, web fetch covers reading documentation pages, following a link from a search result, retrieving a linked issue/PR/spec, or pulling a raw file from a URL the user pasted into the conversation. It sits alongside file read as a "bring external content into context" primitive, but crosses a trust boundary file read does not: the content and the network round-trip are both outside the harness's control.

Web fetch is typically a narrow, read-oriented primitive — "fetch this URL and give me back readable content" — rather than a general-purpose HTTP client with a configurable method, headers, or body.

## Design considerations

**The risk isn't that the fetch mutates anything — it's what the fetch touches, and what it returns.** An arbitrary URL fetch can have side effects on the *remote* system (a GET against a webhook, a URL containing a mutating query string), even though it's a read from the harness's own perspective. And content returned from an arbitrary URL is attacker-controllable if the URL or its response is influenced by anything outside the operator's control (a search result, a file the model read, a webhook response) — a fetch result becomes untrusted input to the model's next turn, i.e. a prompt-injection vector, independent of whether the fetch itself mutates anything.

**A gap worth designing around deliberately: treating "it's just a read" as needing no gate at all.** A URL-fetch tool with no approval gate whatsoever, alongside a shell tool that does have one, is a real, previously-exploited data-exfiltration vector — the fix is to gate fetch like any other network-egress point, not to assume read-only implies safe-by-default.

**Domain allow/deny lists and hard numeric limits (response size, timeout, redirect count) are a common, worthwhile `Configure`-time capability boundary** for a fetch provider, distinct from secrets — the same pattern any capability-scoped provider (a filesystem root, a shell sandbox policy) needs.

## Permission, sandbox & risk classification

`web_fetch` is classified `kind = data_source`, `risk = read_only, conditionally` — conditional because the reference operation MUST be GET-only (no request body, no non-idempotent methods) to justify that classification; a provider wanting POST/PUT/DELETE capability MUST expose that as a separately-named `resource` operation rather than folding it into `web_fetch`. See [`reference-catalog.md`](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls) for the full reasoning.

No OS-level sandbox typically isolates a fetch tool's own outbound network call the way sandboxes commonly isolate shell execution — the fetch tool talks to the network directly, gated only by policy and, where present, a domain allowlist. Network sandboxing generally lags filesystem/process sandboxing for this reason: a harness-initiated outbound call made directly by a tool (rather than via a shelled-out process) often escapes an OS-level sandbox's scope entirely.
