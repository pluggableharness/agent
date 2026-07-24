# RenderTree

The display-agnostic intermediate representation every category's optional `Render` RPC returns — [`model/protocol.md#render`](../model/protocol.md#render), [`tool/protocol.md#render`](../tool/protocol.md#render), and the equivalent sections in `context/` and `memory/` all return exactly this type. A frontend paints it; nothing upstream of `Render` needs to know how. This document is the canonical, standalone definition; every other category's `Render` section links back here rather than re-describing the shape.

The wire type is deliberately factored into its own vocabulary, separate from both the frontend and widget protocols — both the frontend provider protocol ([`frontend-protocol.md`](frontend-protocol.md)) and the widget provider protocol ([`widget-protocol.md`](widget-protocol.md)) place content into the same `Region` enum, and neither category should depend on the other's definitions.

## RenderTree

```protobuf
// RenderTree is the return type of every category's Render() RPC. The tree
// root is just a node, so RenderTree wraps a single root RenderNode — this
// gives every Render() RPC across every category one stable, named response
// type, with room to grow (e.g. a schema_version) without changing
// RenderNode itself.
message RenderTree {
  RenderNode root = 1;
}
```

`RenderNode` is a `oneof` — exactly one variant is set per node:

```protobuf
message RenderNode {
  oneof node {
    TextNode text = 1;
    CodeBlockNode code_block = 2;
    DiffNode diff = 3;
    TableNode table = 4;
    LinkNode link = 5;
    ListNode list = 6;
    GroupNode group = 7;
    CollapsibleNode collapsible = 8;
    SubSessionNode sub_session = 9;
    ActionNode action = 10;
  }
}
```

Recursion happens via `ListNode.items`, `GroupNode.children`, and `CollapsibleNode.children`; every other variant is a leaf.

A frontend **MUST render every node type gracefully** — falling back to a reasonable generic treatment (e.g. a `diff` rendered as plain before/after text on a frontend with no diff view) for a node type it doesn't have a specialized widget for, including a variant added to this enum after that frontend shipped — rather than erroring or silently dropping content it doesn't recognize. This is what makes `RenderTree` genuinely frontend-agnostic rather than TUI-shaped in practice.

### Node types

| Node | Fields | Notes |
|---|---|---|
| `TextNode` | `content: string`, `style: TextStyle?` | Plain or styled text, the most common leaf. `style` unset means "frontend's own default"; `TEXT_STYLE_NORMAL` is a producer *explicitly* requesting plain styling, a distinct state from unset. |
| `CodeBlockNode` | `language: string?`, `content: string` | `language` unset means no syntax highlighting. |
| `DiffNode` | `hunks: []DiffHunk` | Unified-diff shaped. A frontend with no diff view MUST fall back to plain before/after text rather than dropping the node. |
| `TableNode` | `headers: []string`, `rows: []TableRow` | Deliberately flat — string cells only, no nested `RenderNode` per cell. |
| `LinkNode` | `text: string`, `url: string` | A hyperlink. |
| `ListNode` | `items: []RenderNode`, `ordered: bool` | Ordered (numbered) or unordered (bulleted). |
| `GroupNode` | `children: []RenderNode` | A plain, transparent container — no implied wrapper (border, indentation, label) beyond what the frontend chooses to apply. |
| `CollapsibleNode` | `summary: string`, `children: []RenderNode`, `collapsed_by_default: bool` | A labeled container with a default expanded/collapsed state. |
| `SubSessionNode` | `session_id: string`, `summary: string` | The reserved node type for a nested agent transcript reference (e.g. a `RunSession`-spawned child) — rendered as a pointer to that session rather than inlining its full content. See [`kernel-callbacks.md`](../kernel-callbacks.md) and [`agent-loop/subagents.md`](../agent-loop/subagents.md). |
| `ActionNode` | `id: string`, `label: string`, `tool_name: string`, `args: JSON` | An interactive/clickable element — see [Interactive content: the `action` node](#interactive-content-the-action-node) below. |

`DiffHunk` mirrors a standard unified-diff hunk header (`@@ -old_start,old_lines +new_start,new_lines @@`) plus its lines, each tagged `DIFF_LINE_OP_CONTEXT` / `_ADD` / `_REMOVE`:

```protobuf
message DiffHunk {
  int32 old_start = 1;
  int32 old_lines = 2;
  int32 new_start = 3;
  int32 new_lines = 4;
  repeated DiffLine lines = 5;
}
```

### `TextStyle`

`TextNode.style`'s full value set — a frontend with no visual distinction for a given style MUST still render the underlying text rather than dropping it, the same graceful-fallback obligation the node-type table states above:

```protobuf
TextStyle = enum {
  normal    // explicitly plain — distinct from style being unset entirely
  bold
  italic
  code      // inline code/monospace, distinct from a full CodeBlockNode
  dim       // de-emphasized, e.g. secondary/auxiliary information
  error     // something went wrong
  warning   // worth the operator's attention, short of an error
  success   // a positive/completed outcome
}
```

### Interactive content: the `action` node

`ActionNode` is what makes a `RenderTree` interactive, not just displayed — added specifically so a widget could present something the operator clicks or activates, rather than only passive display state:

```protobuf
message ActionNode {
  string id = 1;
  string label = 2;
  string tool_name = 3;
  google.protobuf.Struct args = 4;
  string provider = 5;
}
```

`provider` is the declared name of the tool provider plugin `tool_name` belongs to — `tool_name` is only unique *per provider*, not globally, matching the same naming precedent already established by [`plan/protocol`](../agent-loop/plan-apply-gate.md)'s `PlanItem.provider`. A frontend rendering an `ActionNode` **MUST** make it interactive (a clickable button, a keybindable list item, whatever fits its own UI) and, on activation, **MUST** dispatch a `ClientEvent.action_trigger` carrying that node's `tool_name`/`args`/`provider` unchanged ([`frontend-protocol.md#client-events`](frontend-protocol.md#client-events)). The kernel then handles the resulting `action_trigger` through the normal `tool.v1` `Invoke`/plan-apply pipeline, including policy evaluation, with no model turn — the same no-model-turn dispatch shape a direct-invoke slash command's own `SlashCommandService.Invoke` ([`../slashcommand/protocol.md#invoke`](../slashcommand/protocol.md#invoke)) takes. No action-specific dispatch mechanism exists beyond this — `action` nodes are a second way to *reach* that same dispatch shape, via a click instead of typed text.

This generalizes past widgets for free, since any producer's `Render` output can include an `ActionNode`, not only a widget's: a `tool_result` diff could include an action offering "undo this change," a memory record could offer "forget this," and so on. Widgets are simply the category that motivated adding it — see [`widget-protocol.md#interactive-widgets`](widget-protocol.md#interactive-widgets) for the widget-specific angle.

## Placement & regions

Every `RenderTree` is shown somewhere. `PlacedContent` pairs a tree with where it goes and how it interacts with that region's prior content from the same producer:

```protobuf
enum Region {
  REGION_UNSPECIFIED = 0;
  REGION_MAIN_CHAT = 1;
  REGION_SIDEBAR = 2;
  REGION_TOP_BAR = 3;
  REGION_INPUT_BAR = 4;
  REGION_HOTKEY_HINTS = 5;
  REGION_OVERLAY = 6;
}

message PlacedContent {
  Region region = 1;
  RenderTree content = 2;
  bool replace = 3;    // true: replace this producer's prior content in
                        // `region`; false: append (the default for
                        // REGION_MAIN_CHAT)
  optional int32 priority = 4;  // ordering/eviction hint; unset = declaration order
}
```

**Every region is plugin-contributable** — there is no region reserved as pure, non-extensible chrome. This vocabulary is deliberately abstract: a hypothetical future web or voice frontend isn't required to have a right sidebar. The reference TUI ([`examples.md#the-reference-tui`](examples.md#the-reference-tui)) is *one* conforming implementation of this vocabulary, not the protocol itself.

- **`main_chat`** is the ordinary conversation flow — messages, tool calls, tool results. This is where content lands by default when a producer emits without specifying a region at all.
- **`top_bar`**, **`hotkey_hints`**, **`input_bar`** are typically small, single-producer spaces; a frontend SHOULD apply `priority` to decide what's visible when multiple producers compete for limited room.
- **`overlay`** is for content that should visually interrupt (a modal confirmation, an inline `ask`-decision prompt per [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md)) — a frontend **MUST** render `overlay` content in a way that's visually distinct from ambient `main_chat`/`sidebar` content, even if the specific implementation (a floating pane, a full-screen takeover) is its own choice.

A frontend that lacks a given region (e.g. a plain line-based CLI with no sidebar) MAY silently drop `PlacedContent` targeting it, or fold it into another region as a fallback (e.g. sidebar content appended to `main_chat` instead) — placement is always a hint the frontend is free to reinterpret for its own layout, never a mandate the producer can rely on being honored literally. Multiple producers MAY target the same region with `replace: true`; the frontend orders/allocates space among them by `priority` rather than treating the region as a single-writer slot — coexistence, not exclusivity, is the default, and no config-load-time conflict is raised over two producers sharing a region.

## Schema versioning

Every category's own local `RenderRequest` message (`model/v1/rpc_request.proto`, `tool/v1/rpc_request.proto`, `context/v1/rpc_request.proto`, `memory/v1/rpc_request.proto` each declare their own — see each category's `protocol.md#render`) carries a `string schema_version = 2;` field alongside the opaque `payload` this document's package comment and `.claude/rules/grpc.md`'s Emit/Render carve-out already describe. This section is the canonical, single definition of what that string means; every category's `Render` section links back here rather than re-explaining it.

`schema_version` is round-tripped, not invented at `Render` time. A producer sets it once, at emit time, on [`kernel-callbacks.md#emit`](../kernel-callbacks.md#emit)'s `EmitRequest.schema_version` — "versions the shape of `payload`" — and the kernel persists it verbatim alongside that event's opaque `payload` byte-for-byte, per [`state-backend.md`](../state-backend.md)'s `events.schema_version` column. When the kernel later calls that category's `Render` RPC (live, or as part of a historical replay), it passes the *same* string back on `RenderRequest.schema_version` — the value is never recomputed, guessed, or defaulted by the kernel at render time, only threaded through unchanged from what the producer originally declared.

This is what makes the "supersedes" replay model ([`architecture.md#versioning--schema-drift--supersedes`](../architecture.md#versioning--schema-drift--supersedes)) actually work across a payload-shape change, not just a plugin-version change: a plugin build MAY change what bytes its opaque `payload` contains between releases (a genuine schema evolution, not merely a bug fix), and `schema_version` is how that same build's `Render` implementation tells old-shaped payloads apart from new-shaped ones without having to sniff the bytes. A plugin's `Render` implementation MUST branch on `schema_version` (not attempt to auto-detect a payload's shape from its bytes) whenever it has ever changed its `payload` shape across a released version, and MUST continue to decode every `schema_version` it has ever emitted for as long as any retained session references it — the same permanence guarantee `.claude/rules/proto.md`'s wire-compatibility rule already places on the proto message shapes themselves, applied one level down to the opaque bytes those messages carry.

`schema_version` is a plain string, not an integer or a proto package version — a producer is free to use whatever scheme fits its own release cadence (`"1"`, `"2024-03-shape"`, semver, ...); the kernel and this protocol series never parse or compare it, only store and echo it back. A producer that has never changed its payload shape MAY use a single constant value indefinitely; there is no requirement to bump it on every release, only on a release that actually changes what `payload`'s bytes mean.
