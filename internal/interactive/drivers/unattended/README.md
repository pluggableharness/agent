# internal/interactive/drivers/unattended

The tracked-deviation [`interactive.Resolver`](../../) for a kernel build with no frontend: it refuses every `kind: interactive` tool call rather than fabricating an answer.

## Why it exists

[`docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls`](../../../../docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls) and [`docs/specifications/frontend/frontend-protocol.md`](../../../../docs/specifications/frontend/frontend-protocol.md) require an allowed interactive call's execution to surface as an `interactive_request`/`interactive_response` round trip with a human over an attached frontend. No frontend attach path exists in this codebase yet, so this stage cannot ask a human anything.

That gap is deliberate and operator-approved. This driver exists so it is structurally impossible to mistake for the real behavior: every call returns `interactive.ErrNoFrontend`, and the driver you must name to get that behavior is called `unattended`.

## The auto-refuse / auto-allow asymmetry

The sibling tracked deviation — `internal/plandecision`'s `autoallow` driver — stands in for the *same* missing frontend at the plan/apply gate's `ask` decision, and it auto-**approves**. This one auto-**refuses**. That is not an inconsistency:

| | `plandecision/drivers/autoallow` | `interactive/drivers/unattended` |
|---|---|---|
| Behavior with no frontend | Approves the `ask` item | Refuses the interactive call |
| Is there a defensible default answer? | Yes — execute the call the model already proposed | **No** — the answer *is* a human's words; there is nothing to invent |
| Construction gate | Explicit "acknowledge this is unsafe" argument | None — and its absence is deliberate |
| Why | Auto-approving mutations is genuinely dangerous, so it must be opted into loudly | Refusing is the safe default; fabricating an answer would be a lie told to the model in its own history |

So the missing acknowledgment gate here is **not an oversight**. This driver isn't unsafe — it's simply honest about having nothing to answer with. Adding a gate "for symmetry" would imply a risk that doesn't exist.

## What the caller does with a refusal

`Resolve` returns `interactive.ErrNoFrontend`. The caller — the future tool scheduler, not built here — converts it into a `TOOL_ERROR_CATEGORY_PERMISSION_DENIED` `ToolError` (`pkg/tool/proto/v1`), so the model observes the refusal in its own history and can adapt on a later turn rather than having the call silently vanish. That mirrors the plan/apply gate's own deny path.

## Behavior details

- **Construction logs one INFO**, not WARN: a build with no frontend refusing interactive calls is that build's expected, safe behavior, not a risk being taken. `New` always succeeds.
- **Each `Resolve` logs one WARN** naming the refused tool and call id. Repeated refusals within a session are the signal worth surfacing — a session that keeps hitting interactive calls with nothing able to answer them is one that would benefit from a frontend being attached.
- **Cancellation wins.** An already-done `ctx` returns `ctx.Err()` (checked first, before anything else), never `ErrNoFrontend` — a caller unwinding a canceled turn is never told the reason was a missing frontend.
- **Instrumentation**: the `interactive.resolve` span (`telemetry.Provider.StartInteractiveResolve`) plus the `InteractiveResolutions` counter, tagged `tool.name` and `outcome=error`. A nil `Provider` skips both rather than panicking.
