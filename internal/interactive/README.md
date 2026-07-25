# internal/interactive

The kernel-side seam through which a `kind: interactive` tool call gets its answer.

## What it is

[`docs/specifications/tool/protocol.md#kind-interactive`](../../docs/specifications/tool/protocol.md#kind-interactive) defines `interactive` as a genuine third `ToolKind` alongside `resource` and `data_source`: a call that neither mutates state nor performs a pure read, but blocks the current turn on a human response, whose answer *becomes* the tool's result. `ask_user` is the canonical example.

[`docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls`](../../docs/specifications/agent-loop/plan-apply-gate.md#data-source-and-interactive-calls) specifies what happens once policy has allowed such a call: execution surfaces as [`docs/specifications/frontend/frontend-protocol.md`](../../docs/specifications/frontend/frontend-protocol.md)'s `interactive_request`/`interactive_response` `ServerEvent`/`ClientEvent` pair, correlated by call id, executed strictly sequentially (asking a human two things at once in one frontend is inherently confusing, and `ConcurrencySpec` MUST NOT even be declared for an `interactive` operation).

`Resolver` is the one-method interface standing exactly where that round trip happens:

```go
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Response, error)
}
```

`Request` carries the `CallID` a frontend echoes back for correlation, the `ToolName`, the parsed `Arguments`, and an optional `Prompt` — the `render.v1.RenderTree` a frontend would show a human, built from the originating provider's `Preview` RPC if it implements one, and nil otherwise. `Response` carries the `Payload` that becomes the call's `ToolResult.payload`; validating it against the operation's declared `output_schema` is the caller's job, not this package's.

## Why the seam exists before the frontend does

No frontend attach path exists in this codebase yet. Rather than leave the interactive branch of the future tool scheduler unwritten — or, worse, let it grow a temporary shortcut that silently fabricates answers — the seam is defined now, against the real spec contract, with two implementations:

| Driver | What it does | Status |
|---|---|---|
| [`drivers/unattended`](drivers/unattended/) | Refuses every call with `ErrNoFrontend` | The tracked, operator-approved deviation — see below |
| [`drivers/fake`](drivers/fake/) | Returns a scripted `Response` or error, recording every request | Test-only |
| `drivers/frontend` | Emits `interactive_request`, blocks on `interactive_response` | **Not built** — the spec-correct implementation, once a frontend attach path exists |

Adding the real driver is a new sub-package plus one line in [`drivers/drivers.go`](drivers/drivers.go)'s switch. Nothing else in the kernel branches on a driver name.

## The tracked deviation, and how it differs from its sibling

`drivers/unattended` exists because this stage cannot ask a human anything. It returns `ErrNoFrontend` for every call. The caller — the future tool scheduler, not built here — converts that into a `TOOL_ERROR_CATEGORY_PERMISSION_DENIED` `ToolError` (`pkg/tool/proto/v1`), so the model observes the refusal in its own history and can adapt on a later turn, rather than the call silently vanishing. That mirrors the plan/apply gate's own deny path: "denial surfaces as tool-result text, not a separate out-of-band channel."

This is the second half of the same approved deviation `internal/plandecision`/`drivers/autoallow` represents — both stand in for the same missing frontend. They are deliberately **not** symmetric:

- `plandecision`'s `autoallow` auto-**approves**. An `ask`-decision plan item has a defensible default (execute the call the model already proposed), which is why that driver gates its own construction behind an explicit acknowledgment that doing so is unsafe.
- `interactive`'s `unattended` auto-**refuses**. An interactive call has no such default: its entire payload is a human's answer. Any synthetic answer is a lie told to the model in its own history, and no acknowledgment flag makes fabricating one acceptable. There is no "auto-allow" equivalent for a call whose whole point is asking a human something.

So `unattended.New` has no acknowledgment gate and cannot fail. Its absence is the design, not an omission — refusal is the safe default here, not a risk being taken.

## What this package does not do

- **No policy.** An `interactive` call is policy-prechecked *before* it reaches this seam, through the same non-interactive `allow`/`deny`-only lane `data_source` calls use (policy's own `Match.Kind` stays two-valued in v1). By the time a `Resolver` sees a call, it has already been allowed.
- **No scheduling.** Sequential execution of interactive calls is the tool scheduler's responsibility; a `Resolver` handles one call at a time and knows nothing about the turn around it.
- **No `output_schema` validation.** The caller validates `Response.Payload` against the originating operation's declared schema.
