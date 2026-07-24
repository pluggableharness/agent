# Widget provider — protocol

The widget provider protocol: a plugin that contributes content *into* whichever frontend is attached, without owning the terminal/window/voice channel itself. A git-status panel or a context-budget indicator is the canonical example — content that isn't naturally "a tool" or "a context provider," it just wants to put something on screen.

## Transport

Subprocess + gRPC via `hashicorp/go-plugin`. A widget provider plugin exposes three RPCs: `GetCapabilities`, `Configure`, `Attach`.

**Unlike the frontend provider's bidirectional `Attach` ([`frontend-protocol.md#transport`](frontend-protocol.md#transport)), this `Attach` is server-streaming only.** Widgets are passive/display-only in v1 — a widget wanting to trigger an action (not just display state) does so by *also* being a tool provider with a slash command ([`frontend-protocol.md#slash-commands`](frontend-protocol.md#slash-commands)), not through this channel. Sharing the RPC name `Attach` with the frontend protocol while having a genuinely different streaming shape is a real gotcha worth stating plainly: **frontend `Attach` is bidi, widget `Attach` is not.**

```protobuf
service WidgetService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Attach(AttachRequest) returns (stream WidgetUpdate);
}
```

`GetCapabilities` **MUST** be cheaply re-queryable and **MUST NOT** require a network call, the same guarantee every other category's capability RPC carries. It returns which regions this widget intends to contribute to and its config schema:

```protobuf
message WidgetCapabilities {
  repeated pluggableharness.agent.render.v1.Region regions = 1;  // MUST — see render-tree.md#placement--regions
  pluggableharness.agent.config.v1.ConfigSchema config_schema = 2;
}
```

`Configure` follows the same contract as [`model/protocol.md#configure`](../model/protocol.md#configure): config decoded from the widget's `agent.hcl` block, rejected with a structured error at configure time rather than deferred, never echoing a received secret back out.

`Attach` opens a server-streaming feed of this widget's rendered updates for one session:

```protobuf
message AttachRequest {
  string session_id = 1;
}

message WidgetUpdate {
  pluggableharness.agent.render.v1.Region region = 1;
  pluggableharness.agent.render.v1.RenderTree content = 2;
  bool replace = 3;  // true: replace this widget's prior content in `region`; false: append
}
```

This stream is purely how the widget pushes its rendered updates out; it never receives anything back on this channel — there is no equivalent of the frontend protocol's `ClientEvent`. Cancellation is the kernel closing the gRPC stream; the plugin **MUST** treat this as normal control flow, never as an error, the same cancellation discipline every server-streaming RPC in this protocol series requires ([`model/README.md#transport--lifecycle`](../model/README.md#transport--lifecycle)).

## Deriving display state — no new data feed

A widget provider gets no special session-state API. It is implicitly available to subscribe to hook points in `observe` mode ([`agent-loop/hook-dispatch.md`](../agent-loop/hook-dispatch.md)) — exactly the same mechanism a cross-cutting audit-logger already uses — and derives whatever it wants to display from the events it observes:

- A context-budget indicator watches `post-model-response`'s usage figures.
- A git-status panel watches `post-tool-call` for filesystem-provider writes.

`Attach`'s `WidgetUpdate` stream is how the *result* of that observation reaches the frontend; `observe`-mode hook subscription is how the widget *derives* it in the first place. No parallel session-state feed exists alongside hook dispatch for this purpose — reusing the existing mechanism was a deliberate choice over inventing a second one.

## Interactive widgets

A widget's `WidgetUpdate.content` **MAY** include [`ActionNode`s](render-tree.md#interactive-content-the-action-node) the same way any other `RenderTree` can — no widget-specific protocol addition was needed beyond the general `action` node mechanism ([`render-tree.md#interactive-content-the-action-node`](render-tree.md#interactive-content-the-action-node)). A clickable sidebar item (a widget offering "dismiss," "retry," or "open in editor," for example) is expressed exactly like an action node contributed by any other producer: the frontend renders it interactive, and on activation dispatches `ClientEvent.action_trigger` ([`frontend-protocol.md#client-events`](frontend-protocol.md#client-events)), which the kernel handles identically to a `direct_invoke` slash command — the normal `Invoke`/plan-apply pipeline, including policy evaluation, with no model turn.

This was the specific gap that motivated adding `ActionNode` to [`render-tree.md`](render-tree.md) in the first place — widgets needed a way to trigger something, not just display state — but the resulting mechanism generalizes past widgets: any producer's rendered content can offer a one-click follow-up action, not only widget-contributed panels.
