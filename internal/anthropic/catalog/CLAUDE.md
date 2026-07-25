# internal/anthropic/catalog — agent notes

## Never write a number here from memory

Every figure in `catalog.go` — model ID, context window, output ceiling, and above all every rate — MUST come from Anthropic's own current published documentation, fetched at the time of the edit. Not from recall, not from another file in this repo, not from a training-data prior about what Claude models cost.

This is stricter than ordinary care because of where the numbers end up. The kernel computes `cost_usd` from `Pricing` **the moment each `usage` event arrives** and persists the dollar amount into `cost_ledger` ([`protocol.md#cost-computation`](../../../docs/specifications/model/protocol.md#cost-computation)). Nothing ever recomputes it. A rate that is wrong today produces ledger rows that stay wrong forever, in every session that ran against it, with no error anywhere to notice it by. [`determinism.md`](../../../.claude/rules/determinism.md) treats that as a correctness bug.

The `sourcedOn` constant records when the table was last verified. If it is materially stale and you are touching this file, re-verify the whole table and move the date — don't edit one row against fresh data and leave the rest carrying an old date's authority.

Sources: `https://platform.claude.com/docs/en/about-claude/models/overview` (roster, context windows, output ceilings) and `https://platform.claude.com/docs/en/about-claude/pricing` (every rate, including the cache and batch columns).

## Model IDs are pinned snapshots, not aliases

From the 4.6 generation onward, `claude-opus-5` and friends are dateless **and pinned** — they are not evergreen pointers that silently move to a newer model. Do not "modernize" an ID by appending a date suffix (`claude-opus-5-20260609`); that is a 404. Older models (Haiku 4.5) do have dated IDs, and their bare alias resolves to the dated one — `claude-haiku-4-5` is correct and preferred.

## Two ThinkingSpec judgment calls worth not re-litigating

Both are explained in full in `catalog.go`'s own comments; this is the short form so a reviewer knows they were deliberate.

- **Fable 5 is `DISCRETE_EFFORT` with `CanDisable: false`, not `ALWAYS_ON_ADAPTIVE`.** Its reasoning genuinely cannot be switched off, which is what `ALWAYS_ON_ADAPTIVE` describes — but that mode also means "no caller-selectable effort level", and Fable 5 *does* expose the full effort ladder. `DISCRETE_EFFORT` + `CanDisable: false` carries both facts; the other choice carries only one.
- **Opus 5 declares `CanDisable: true` even though disabling is effort-conditional.** Anthropic accepts `thinking: {type: "disabled"}` only at effort `high` or below. The protocol has no field for a conditional disable, and `false` would be the larger lie — it would deny a control that exists across three of the five effort levels.

## Only the 5-minute cache-write rate is quoted

Anthropic publishes two cache-write rates (5-minute at 1.25x input, 1-hour at 2x). `PricingTier` has exactly one `CacheWritePerMtok` field, and the adapter never sets a `ttl` on a breakpoint, so 5-minute is the only rate this plugin can actually incur. Quoting the 1-hour rate would overstate every cached turn by 60%. If the adapter ever gains 1-hour breakpoints, that needs a protocol change, not a quiet edit to this number.

## Adding a model

1. Verify the full table against the two live doc pages above; update `sourcedOn`.
2. Add the constructor next to its generation-mates and register it in `Models()`, keeping the newest-first ordering.
3. Run `go test ./internal/anthropic/catalog/...`. The tests are transcription guards, not restatements — a broken cache/batch ratio or a non-parsing tier window means a typo, not a test that needs relaxing. Fix the number, never the assertion.
