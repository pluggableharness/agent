# Frontend provider — protocol

The frontend provider protocol: the plugin that owns the terminal (or window, or voice channel), attaches to a session's live event stream, and turns operator input into `ClientEvent`s. See [`README.md#transport--lifecycle`](README.md#transport--lifecycle) for how this category's `Attach` shape differs from the widget provider's.

## Transport

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). A frontend provider plugin exposes three RPCs: `GetCapabilities`, `Configure`, `Attach`.

**`Attach` is a bidirectional stream** — the one genuinely bidi RPC in this directory, and one of only two in the entire protocol series (the other being the kernel-callback channel). Every other category's primary RPC (`StreamCompletion`, `Invoke`) is server-streaming-plus-cancellation; the widget provider's own `Attach` ([`widget-protocol.md#transport`](widget-protocol.md#transport)) is server-streaming only despite sharing the RPC name. A frontend genuinely needs both directions live on one connection, unlike a model or tool provider: the operator can type a message while prior content is still rendering, so `ClientEvent`s and `ServerEvent`s must be able to cross in flight.

```protobuf
service FrontendService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Attach(stream ClientEvent) returns (stream ServerEvent);
}
```

`GetCapabilities` returns this frontend's `slash_commands` (see [Slash commands](#slash-commands) below) and `ConfigSchema`; it MUST be cheaply re-queryable and MUST NOT require a network call, the same guarantee [`provider/protocol.md#getcapabilities`](../provider/protocol.md#getcapabilities) requires of a model provider. `Configure` follows the same contract as [`provider/protocol.md#configure`](../provider/protocol.md#configure): a config value decoded from the provider's `agent.hcl` block via the schema-to-cty bridge, rejected with a structured error at configure time rather than deferred to the first `Attach`, and never echoing a received secret back out through any event, render, or log line.

## Fast path vs. full render

Live token-by-token text streaming (a model provider's `text_delta`, per [`provider/protocol.md#streamcompletion`](../provider/protocol.md#streamcompletion)) does **not** round-trip through a producer's `Render` call per token — that would be far too slow. The kernel forwards raw text deltas to the frontend directly as they arrive, as `ServerEvent.stream_delta`; `Render` is invoked once per producer per logically-complete unit (a finished message, a finished tool result), not per token, and its result arrives as `ServerEvent.render` carrying a [`PlacedContent`](render-tree.md#placement--regions).

```protobuf
message ServerEvent {
  oneof event {
    StreamDelta stream_delta = 1;
    Render render = 2;
    PermissionRequest permission_request = 3;
    PlanReady plan_ready = 4;
    InteractiveRequest interactive_request = 5;
    SessionTreeUpdate session_tree_update = 6;
    Error error = 7;
  }

  message StreamDelta {
    string target_id = 1;  // correlates consecutive deltas into one growing piece of text
    string text = 2;
  }

  message Render {
    pluggableharness.agent.render.v1.PlacedContent content = 1;
  }

  message PermissionRequest {
    pluggableharness.agent.plan.v1.PlanItem plan_item = 1;
  }

  message PlanReady {
    pluggableharness.agent.plan.v1.Plan plan = 1;
  }

  message InteractiveRequest {
    string call_id = 1;
    string tool_name = 2;
    pluggableharness.agent.render.v1.RenderTree prompt = 3;
  }

  message SessionTreeUpdate {
    string parent_session_id = 1;
    string child_session_id = 2;
    pluggableharness.agent.session.v1.SessionStatus status = 3;
  }

  message Error {
    FrontendError error = 1;
  }
}
```

`PermissionRequest` asks the operator to resolve a pending `ask` decision ([`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md)): the kernel blocks that plan item's apply until a matching `ClientEvent.plan_decision` resolves it. `InteractiveRequest` carries a [`kind: interactive`](../tool/protocol.md#kind-interactive) tool call across the frontend boundary — the kernel renders the tool's own prompt content as an ordinary `RenderTree` (in the `overlay` region), and `call_id` correlates the request with the eventual `ClientEvent.interactive_response` the same way `tool_call_id` correlates an ordinary resource call and its result elsewhere in this protocol series. A frontend **MUST** render `InteractiveRequest.prompt` in the `overlay` region ([`render-tree.md#placement--regions`](render-tree.md#placement--regions)), the same visual treatment as an ordinary `ask` prompt.

## Client events

```protobuf
enum ClientDecision {
  CLIENT_DECISION_UNSPECIFIED = 0;
  CLIENT_DECISION_ALLOW = 1;
  CLIENT_DECISION_DENY = 2;
}

message ClientEvent {
  oneof event {
    UserMessage user_message = 1;
    SlashCommand slash_command = 2;
    PlanDecision plan_decision = 3;
    InteractiveResponse interactive_response = 4;
    ActionTrigger action_trigger = 5;
    Interrupt interrupt = 6;
  }

  message UserMessage {
    string text = 1;
  }

  message SlashCommand {
    string name = 1;   // without the leading slash
    string args = 2;   // raw argument string
  }

  message PlanDecision {
    string plan_item_id = 1;
    ClientDecision decision = 2;
    optional google.protobuf.Struct corrected_input = 3;
  }

  message InteractiveResponse {
    string call_id = 1;
    google.protobuf.Struct response = 2;
  }

  message ActionTrigger {
    string node_id = 1;
    string tool_name = 2;
    google.protobuf.Struct args = 3;
  }

  message Interrupt {}
}
```

`ActionTrigger` is what a frontend dispatches when the operator activates a [`RenderTree`'s `ActionNode`](render-tree.md#interactive-content-the-action-node); `node_id`, `tool_name`, and `args` are echoed unchanged from the originating node. The kernel handles the resulting `action_trigger` identically to a `direct_invoke` slash command (below): the normal `Invoke`/plan-apply pipeline, including policy evaluation, with no model turn.

### `plan_decision.corrected_input`

`PlanDecision.corrected_input` is an opencode-style `CorrectedError` redirect: rather than a binary allow/deny, the operator can supply corrected tool arguments and have the kernel treat the item as allowed with those arguments instead of the model's originals. When present, the kernel **MUST** re-validate `corrected_input` against the tool's `input_schema` ([`tool/data-types.md#toolschema`](../tool/data-types.md#toolschema)) before treating the item as allowed — an invalid correction **MUST** be rejected back to the sending frontend as a distinct error, never silently coerced and never silently downgraded to a plain `deny`. This re-validation is mechanically part of the plan/apply gate's decision handling; see [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md) for where it slots into the turn algorithm — this document defines the field and the frontend-facing contract around it, the gate owns applying it.

## Session scope

**Multiple frontends MAY `Attach` to the same session concurrently** — see [`README.md#session-scope--multi-attach`](README.md#session-scope--multi-attach) for why this is the current design, not a 1:1 attachment model. The operational rules:

- **`ServerEvent`s broadcast identically to every attached frontend.** No partitioning, no "primary" frontend — every attached frontend observes the same live stream in the same order.
- **`ClientEvent`s are processed in kernel arrival order.** `user_message`/`slash_command`/`action_trigger`/`interrupt` have no real conflict — multiple frontends sending these just interleave as ordinary sequential input, a legitimate pairing/multi-operator scenario, not an error case.
- **`plan_decision`/`interactive_response` name a specific pending item** (`plan_item_id`/`call_id`). **First response for a given item wins.** Any subsequent response for an already-resolved item **MUST** be rejected back to the sending frontend with a distinct `invalid_client_event`-category error (see [Error taxonomy](#error-taxonomy)), never silently dropped and never silently re-applied — so that frontend's UI can show "already decided elsewhere" rather than appearing to hang.

## Slash commands

`SlashCommandSpec` is defined once, canonically, here — every other category's capability response ([`provider/protocol.md#getcapabilities`](../provider/protocol.md#getcapabilities), [`tool/protocol.md#getschema`](../tool/protocol.md#getschema), and the equivalent sections in `context/` and `memory/`) declares an optional `[]SlashCommandSpec` field of this same type and links back here rather than redefining it. The wire type is factored out into its own shared vocabulary for exactly that reason: it's shared by every provider category's `GetCapabilities`/`GetSchema` response, not owned by the frontend category alone.

```protobuf
enum Dispatch {
  DISPATCH_UNSPECIFIED = 0;
  DISPATCH_DIRECT_INVOKE = 1;
  DISPATCH_PROMPT_EXPANSION = 2;
}

message SlashCommandSpec {
  string name = 1;             // invoked as "/name"; MUST be unique across
                                // every provider loaded in the session
  string description = 2;      // shown in the hotkey_hints region
  Dispatch dispatch = 3;
  optional string tool_name = 4;  // MUST be set iff dispatch == DISPATCH_DIRECT_INVOKE;
                                   // MUST name one of this SAME provider's own
                                   // tool operations
  optional string template = 5;    // MUST be set iff dispatch == DISPATCH_PROMPT_EXPANSION;
                                   // "{arg}"-style placeholders substituted from
                                   // the operator's typed arguments
}
```

A name collision across providers **MUST** be a config-load-time error, per this protocol series' established "ambiguity is an error, not a silent pick" pattern.

- **`DISPATCH_DIRECT_INVOKE`**: the frontend recognizes `/name args`, maps `args` to the named tool's `input_schema`, and dispatches it through the *normal* `Invoke`/plan-apply pipeline ([`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md)) — including policy evaluation — with **no model turn**. This is a real behavior difference from an ordinary tool call: the model never sees or decides on this invocation, only its eventual result (appended to history as an ordinary `tool_result`, so the model has full visibility on the *next* turn even though it didn't initiate this one).
- **`DISPATCH_PROMPT_EXPANSION`**: the frontend expands `template` with the typed arguments and submits the result as an ordinary `ClientEvent.user_message` — this costs a model turn like any normal message; the only thing the slash command bought was not having to type the full instruction out.
- A profile's tool scoping determines which `DISPATCH_DIRECT_INVOKE` commands are available: a command naming a tool absent from the active profile's tool list simply isn't registered for that session. `DISPATCH_PROMPT_EXPANSION` commands have no backing tool to scope against this way, so they're scoped separately, by an explicit `agent_profile.slash_commands` allow-list — see [`configuration/agent-profiles.md`](../configuration/agent-profiles.md) for the block itself.

## Error taxonomy

```protobuf
enum FrontendErrorCategory {
  FRONTEND_ERROR_CATEGORY_UNSPECIFIED = 0;
  FRONTEND_ERROR_CATEGORY_RENDER_FAILED = 1;
  FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT = 2;
  FRONTEND_ERROR_CATEGORY_REGION_UNSUPPORTED = 3;
  FRONTEND_ERROR_CATEGORY_UNKNOWN = 4;
}

message FrontendError {
  FrontendErrorCategory category = 1;
  string message = 2;
}
```

| Category | Meaning | Requirement |
|---|---|---|
| `render_failed` | A specific `RenderTree` node couldn't be painted (e.g. a malformed `diff`). | MUST fall back to a generic text rendering of whatever content is recoverable; MUST NOT crash the frontend process over one bad node. |
| `invalid_client_event` | Malformed input on the operator-facing side — including a `plan_decision`/`interactive_response` naming an already-resolved item (see [Session scope](#session-scope)). | Rare in the ordinary case, since the frontend itself constructs `ClientEvent`s; MUST be surfaced distinctly, not collapsed into `unknown`. |
| `region_unsupported` | A producer targeted a `Region` this frontend has no fallback behavior for at all. | SHOULD be logged; MUST NOT be treated as fatal. |
| `unknown` | Anything else. | Structured error taxonomy applies here as everywhere else in this protocol series — see [`conformance.md`](conformance.md#error-taxonomy). |

`ConfigureResponse` errors surface as a gRPC status carrying a `FrontendError` in its structured detail, not as an in-band field on the response message. Errors encountered mid-`Attach` surface as `ServerEvent.error`.
