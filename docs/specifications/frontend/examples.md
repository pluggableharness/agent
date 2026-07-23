# Frontend & widget — examples

Wire-protocol excerpts for all three schemas this directory covers, a worked frontend `Attach` sequence, a worked widget example, and the reference TUI that instantiates the region vocabulary in practice.

## The wire protocols

`FrontendService`:

```protobuf
service FrontendService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Attach(stream ClientEvent) returns (stream ServerEvent);
}
```

`WidgetService` — note the different `Attach` shape despite the identical RPC name:

```protobuf
service WidgetService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Attach(AttachRequest) returns (stream WidgetUpdate);
}
```

The shared `RenderTree`/`Region` vocabulary:

```protobuf
message RenderTree {
  RenderNode root = 1;
}

enum Region {
  REGION_UNSPECIFIED = 0;
  REGION_MAIN_CHAT = 1;
  REGION_SIDEBAR = 2;
  REGION_TOP_BAR = 3;
  REGION_INPUT_BAR = 4;
  REGION_HOTKEY_HINTS = 5;
  REGION_OVERLAY = 6;
}
```

See [`render-tree.md`](render-tree.md) for the full `RenderNode` variant list and [`frontend-protocol.md`](frontend-protocol.md) / [`widget-protocol.md`](widget-protocol.md) for the full `ServerEvent`/ `ClientEvent`/`WidgetUpdate` shapes these excerpts are trimmed from.

## A worked frontend `Attach` sequence

A TUI frontend attaches to a session already in progress, receives a `plan_ready` event for a proposed file edit, renders it, and the operator approves it with a corrected argument:

```text
→ Attach() opens the bidirectional stream.

← ServerEvent{stream_delta: {target_id: "msg_7", text: "I'll fix the "}}
← ServerEvent{stream_delta: {target_id: "msg_7", text: "off-by-one in main.go."}}
← ServerEvent{
    plan_ready: {
      plan: {
        turn_id: "turn_42",
        items: [{
          id: "item_1", tool_call_id: "tc_9", provider: "filesystem",
          tool_name: "write_file",
          input: {"path": "main.go", "content": "...for i := 0; i <= n; i++..."},
          decision: PLAN_DECISION_PENDING,
        }],
      },
    },
  }
← ServerEvent{
    permission_request: {
      plan_item: { id: "item_1", tool_call_id: "tc_9", provider: "filesystem",
                   tool_name: "write_file", decision: PLAN_DECISION_ASK },
    },
  }
← ServerEvent{
    render: {
      content: {
        region: REGION_OVERLAY,
        content: { root: { diff: { hunks: [{
          old_start: 10, old_lines: 1, new_start: 10, new_lines: 1,
          lines: [
            {op: DIFF_LINE_OP_REMOVE, text: "for i := 0; i <= n; i++ {"},
            {op: DIFF_LINE_OP_ADD,    text: "for i := 0; i < n; i++ {"},
          ],
        }]}}},
        replace: true,
      },
    },
  }

// The operator reviews the diff in the overlay and corrects the fix to
// use "<=" bounded on n-1 instead, rather than accepting or rejecting outright:
→ ClientEvent{
    plan_decision: {
      plan_item_id: "item_1",
      decision: CLIENT_DECISION_ALLOW,
      corrected_input: {"path": "main.go", "content": "...for i := 0; i < n-1; i++..."},
    },
  }

// The kernel re-validates corrected_input against write_file's input_schema
// (tool/data-types.md#toolschema), accepts it, applies the write, and the
// turn continues — the model sees the corrected content's tool_result on
// its next turn, per frontend-protocol.md#plan_decisioncorrected_input.
```

If a second, slower frontend also attached to `turn_42` sends its own `plan_decision` for `item_1` after the one above resolved it, the kernel rejects that second response with a `FrontendError{category: FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT}` back to that frontend alone — per [`frontend-protocol.md#session-scope`](frontend-protocol.md#session-scope)'s first-response-wins rule. The first frontend's approval stands unaffected.

## A worked widget example

A persistent status-bar widget derives context-budget state from the same event stream a frontend already observes, with no new data feed:

```text
// The widget subscribes to post-model-response in observe mode
// (agent-loop/hook-dispatch.md) and watches each usage event go by.
// It never calls CountTokens itself — the kernel's own hook payload
// already carries the resolved usage figures.

← WidgetUpdate{
    region: REGION_TOP_BAR,
    content: { root: { text: { content: "estimated used: 51,204 / 200,000 tokens",
                                style: TEXT_STYLE_DIM } } },
    replace: true,
  }

// A later turn crosses a configured budget-warning threshold; the widget
// pushes a replacement update using TEXT_STYLE_WARNING instead, and adds a
// one-click follow-up via an ActionNode — the "interactive widget" case
// (render-tree.md#interactive-content-the-action-node):

← WidgetUpdate{
    region: REGION_TOP_BAR,
    content: { root: { group: { children: [
      { text: { content: "82% of context budget used", style: TEXT_STYLE_WARNING } },
      { action: { id: "act_compact", label: "Compact now",
                  tool_name: "compact_context", args: {} } },
    ]}}},
    replace: true,
  }

// Activating "Compact now" in the frontend dispatches:
→ ClientEvent{action_trigger: {node_id: "act_compact", tool_name: "compact_context", args: {}}}

// The kernel handles this exactly like a direct_invoke slash command: the
// normal Invoke/plan-apply pipeline, no model turn, result appended to
// history as an ordinary tool_result.
```

Note the widget never received a `ClientEvent` of its own — the `action_trigger` above flows from the *frontend's* `Attach` stream, not the widget's. The widget only ever pushes `WidgetUpdate`s outward.

## The reference TUI

Not protocol, but a concrete instantiation worth documenting since it's the harness's primary interface: a full-screen terminal takeover, OpenCode-style.

| Region | Reference layout |
|---|---|
| `top_bar` | Single line: session/profile name, active model, context-budget indicator |
| `main_chat` | The dominant, scrollable area — conversation flow, tool calls/results, collapsed `sub_session` nodes |
| `sidebar` | Right-hand column — widget-contributed panels (git status, background job state, etc.), stacked by `priority` |
| `input_bar` | Bottom-anchored — the operator's message composer |
| `hotkey_hints` | Directly above or beside `input_bar` — a compact reminder of available slash commands / keybindings |
| `overlay` | Full-screen or floating-pane takeover for `ask`-decision prompts and other interrupting content |

The reference TUI **MUST** implement [`render-tree.md#placement--regions`](render-tree.md#placement--regions)'s graceful-fallback rule for any region it chooses not to visually distinguish (e.g. a narrow terminal collapsing `sidebar` into a toggleable pane rather than a permanent column) — the protocol only requires that placement be *honored or gracefully reinterpreted*, never that every region get permanent, dedicated screen real estate. This layout is not normative: it is one conforming implementation of the abstract region vocabulary, not a constraint on any other frontend.
