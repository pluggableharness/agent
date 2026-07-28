# RenderTree intermediate representation

`RenderTree` is the intermediate representation returned by every category's optional `Render` RPC (model, tool, context, memory) and painted by frontends as **transcript** content. It is deliberately **not** a placement system.

## Placement is not this package's job

There is no `Region` enum and no `PlacedContent` wrapper. State, metadata, and input are typed kernel surfaces ([`frontend/README.md`](README.md#four-surfaces)). Only conversation transcript content travels as `RenderTree`.

## RenderTree / RenderNode

`RenderTree` wraps a single root `RenderNode`. The tree root is just a node; multi-root content is a `Group`, `List`, or `Collapsible`.

Node variants (every frontend MUST render every variant gracefully, with a generic fallback — never error, never silently drop):

| Node | Role |
|---|---|
| `Text` | Plain or styled text |
| `CodeBlock` | Source with optional language hint |
| `Diff` | Unified-diff shaped hunks; no-diff frontends fall back to plain before/after text |
| `Table` | Flat string cells |
| `Link` | Text + URL |
| `List` | Ordered or unordered children |
| `Group` | Transparent container |
| `Collapsible` | Summary + children, default expanded/collapsed |
| `SubSession` | Nested agent transcript pointer |
| `Action` | Interactive control; activation → `TriggerAction` with tool_name/args/provider unchanged |

## Schema versioning for opaque Emit payloads

Producers that `Emit` then optionally `Render` version their opaque payload via `schema_version` on the emit envelope. Frontends that only paint `RenderTree` never need the opaque bytes.

## ActionNode

`ActionNode` carries `id`, `label`, `tool_name`, `args`, and `provider` (tool names are unique per provider). On activation the frontend calls `KernelCallbackService.TriggerAction`; the kernel runs the normal Invoke/plan-apply pipeline with **no model turn**.
