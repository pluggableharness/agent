# User Question / Elicitation

## What it is

User question / elicitation is the tool-shaped mechanism by which a model mid-turn asks a human a clarifying question and blocks until it gets an answer — a structured escape hatch from the otherwise autonomous think-act-observe loop. Typical shapes are a single free-text prompt or a small set of selectable multiple-choice options (2-5 choices is a common range), rendered by the harness's UI layer and returned to the model as the tool's result once the human responds.

It sits at a specific point in a coding agent's workflow: after the agent has enough context to know it's missing a decision only a human can make (which of two refactor approaches, which environment to target, whether to proceed past a risky step), but before it commits to an irreversible path. It is distinct from *permission approval* (the harness asking the human whether an action is allowed) — here the *model* is the one asking, and the answer shapes subsequent reasoning rather than gating a specific mutating call.

Nearly every design that has this capability exposes it as an ordinary tool in the same function-calling namespace as file/shell/search tools, rather than as a side-channel or special API — "ask a human" behaves like any other tool call from the model's point of view.

## Design considerations

**Blocking vs. non-blocking is a real semantic split.** Most designs block the current turn until the human answers. A less common alternative lets the agent continue working on other steps while the question is outstanding, or resolves automatically after a timeout if nobody answers — useful in a semi-autonomous run, but it changes what "waiting for an answer" means for the rest of the turn.

**The headless/non-interactive gap is a real design hazard.** In a fully autonomous or headless invocation, an `ask_user`-shaped call has nobody to answer it — without an explicit policy for this case, the call simply hangs forever. A robust design routes `interactive` calls through the same allow/deny-only policy precheck a read-only call gets, so an operator can deny interactive prompts outright in a headless context rather than let the session stall.

**Client-surface gating is common.** Whether the tool is even present in the model's tool list can depend on the surface it's running in (a CLI/desktop client vs. a fully unattended cloud/background mode) — a capability-availability decision, not a per-call risk decision.

## Permission, sandbox & risk classification

`ask_user` is classified as its own `kind = interactive` — a genuine third kind alongside `resource` and `data_source`, because the call neither mutates state nor performs a pure read; it blocks on a human response and the answer becomes the result. `risk = read_only` follows automatically, since neither `data_source` nor `interactive` calls have an external blast radius to classify. See [`protocol.md`](../../specifications/tool/protocol.md#kind-interactive) for the full semantics, and [`reference-catalog.md`](../../specifications/tool/reference-catalog.md) for why `ask_user` doesn't fit `resource` or `data_source`.

`interactive` calls MUST NOT go through the resource plan/apply gate (there is nothing to approve, only a question to answer), MUST still pass a policy precheck so headless invocations can deny them outright, and MUST execute sequentially — never concurrently with another `interactive` call in the same turn, since asking a human two things at once is inherently confusing. No sandboxing applies: there is no filesystem or network surface for a pure question-and-answer round trip to isolate.
