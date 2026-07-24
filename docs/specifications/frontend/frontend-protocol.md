# Frontend provider — protocol

The frontend provider protocol: the plugin that owns the terminal (or window, or voice channel), sends operator input to the kernel as `ClientEvent`s over one connection-scoped stream, and receives session lifecycle and content updates back as `ServerEvent`s on the same stream. See [`README.md#transport--lifecycle`](README.md#transport--lifecycle) for how this category's `Attach` shape differs from the widget provider's.

## Transport

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). A frontend provider plugin exposes four RPCs: `GetCapabilities`, `Configure`, `Attach`, `Describe`.

**`Attach` is a bidirectional stream** — the one genuinely bidi RPC in this directory, and one of only two in the entire protocol series (the other being the kernel-callback channel). Every other category's primary RPC (`StreamCompletion`, `Invoke`) is server-streaming-plus-cancellation; the widget provider's own `Attach` ([`widget-protocol.md#transport`](widget-protocol.md#transport)) is server-streaming only despite sharing the RPC name. A frontend genuinely needs both directions live on one connection, unlike a model or tool provider: the operator can type a message while prior content is still rendering, so `ClientEvent`s and `ServerEvent`s must be able to cross in flight.

```protobuf
service FrontendService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Attach(stream ClientEvent) returns (stream ServerEvent);
  rpc Describe(DescribeRequest) returns (DescribeResponse);
}
```

**`Attach` is connection-scoped, with per-session subscription — not one stream per session.** A frontend opens exactly one `Attach` stream per connection and keeps it open for the connection's whole lifetime; individual sessions are subscribed onto that single stream via the session-control `ClientEvent` variants ([Session lifecycle](#session-lifecycle) below), and every event on the wire — both directions — carries a top-level `session_id` field that multiplexes which session it belongs to. This is deliberate, not incidental: full session lifecycle needs connection-level operations (listing every session, the aggregate slash-command registry, creating a session before anything is attached to it) that have no natural home under a strictly per-session stream, and `.claude/rules/grpc.md`'s streaming-shape table permits exactly one genuinely bidirectional frontend RPC — multiplexing preserves that invariant rather than adding a second bidi stream or folding session control into the unrelated kernel-callback channel.

`GetCapabilities` returns this frontend's `slash_commands` (see [Slash commands](#slash-commands) below), `ConfigSchema`, `supported_regions`, and `supported_hook_points`; it MUST be cheaply re-queryable and MUST NOT require a network call, the same guarantee [`model/protocol.md#getcapabilities`](../model/protocol.md#getcapabilities) requires of a model provider. `Configure` follows the same contract as [`model/protocol.md#configure`](../model/protocol.md#configure): a config value decoded from the provider's `agent.hcl` block via the schema-to-cty bridge, rejected with a structured error at configure time rather than deferred to the first `Attach`, and never echoing a received secret back out through any event, render, or log line.

`Describe` reports this plugin build's own identity — `{name, version, source, category, protocol_version}` — directly from the running process, rather than the kernel inferring it from a lock-file row. Every one of the six category protocols gains this identical RPC in this protocol revision; it exists specifically for a `dev_overrides`-resolved binary, which has no `provider {}` lock-file entry to read identity from at all (`configuration/lock-file.md`'s "`dev_overrides` and identity without a lock entry").

## Fast path vs. full render

Live token-by-token text streaming (a model provider's `text_delta`, per [`model/protocol.md#streamcompletion`](../model/protocol.md#streamcompletion)) does **not** round-trip through a producer's `Render` call per token — that would be far too slow. The kernel forwards raw text deltas to the frontend directly as they arrive, as `ServerEvent.stream_delta`; `Render` is invoked once per producer per logically-complete unit (a finished message, a finished tool result), not per token, and its result arrives as `ServerEvent.render` carrying a [`PlacedContent`](render-tree.md#placement--regions).

```protobuf
message ServerEvent {
  // Scopes this event to a session. Set for every session-scoped variant;
  // empty only for the connection-level session_list variant.
  string session_id = 100;

  // Correlates a response to the ClientEvent control message that
  // triggered it (echoing that message's own request_id). Set on
  // session_created/session_attached/backfill_complete/session_detached/
  // session_list, and on error when it answers a control request; unset
  // for ordinary live session events not triggered by a specific client
  // request.
  optional string request_id = 101;

  oneof event {
    StreamDelta stream_delta = 1;
    Render render = 2;
    PermissionRequest permission_request = 3;
    PlanReady plan_ready = 4;
    InteractiveRequest interactive_request = 5;
    SessionTreeUpdate session_tree_update = 6;
    Error error = 7;
    SessionCreated session_created = 8;
    SessionAttached session_attached = 9;
    BackfillComplete backfill_complete = 10;
    SessionDetached session_detached = 11;
    SessionList session_list = 12;
    // 13 is reserved, not assigned — see "No session deletion" below.
    SlashCommandRegistry slash_command_registry = 14;
    UsageUpdate usage_update = 15;
    SessionStatusUpdate session_status_update = 16;
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

`PermissionRequest` asks the operator to resolve a pending `ask` decision ([`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md)): the kernel blocks that plan item's apply until a matching `ClientEvent.plan_decision` resolves it. `InteractiveRequest` carries a [`kind: interactive`](../tool/protocol.md#kind-interactive) tool call across the frontend boundary — the kernel renders the tool's own prompt content as an ordinary `RenderTree` (in the `overlay` region), and `call_id` correlates the request with the eventual `ClientEvent.interactive_response` the same way `tool_call_id` correlates an ordinary resource call and its result elsewhere in this protocol series. A frontend **MUST** render `InteractiveRequest.prompt` in the `overlay` region ([`render-tree.md#placement--regions`](render-tree.md#placement--regions)), the same visual treatment as an ordinary `ask` prompt. `SessionTreeUpdate` reports a **child** session's status change (a `RunSession`-spawned sub-agent); see [Session lifecycle](#session-lifecycle) below for `SessionStatusUpdate`, the parallel variant for the *attached* session's own status.

## Client events

```protobuf
enum ClientDecision {
  CLIENT_DECISION_UNSPECIFIED = 0;
  CLIENT_DECISION_ALLOW = 1;
  CLIENT_DECISION_DENY = 2;
}

enum PlanDecisionScope {
  PLAN_DECISION_SCOPE_UNSPECIFIED = 0;
  PLAN_DECISION_SCOPE_ONCE = 1;
  PLAN_DECISION_SCOPE_SESSION = 2;
  PLAN_DECISION_SCOPE_ALWAYS = 3;
}

message ClientEvent {
  // REQUIRED for user_message..interrupt (session-scoped variants); empty
  // for the connection-level control variants (hello..list_sessions).
  string session_id = 100;

  oneof event {
    UserMessage user_message = 1;
    SlashCommand slash_command = 2;
    PlanDecision plan_decision = 3;
    InteractiveResponse interactive_response = 4;
    ActionTrigger action_trigger = 5;
    Interrupt interrupt = 6;
    Hello hello = 7;
    CreateSession create_session = 8;
    AttachSession attach_session = 9;
    ResumeSession resume_session = 10;
    DetachSession detach_session = 11;
    ListSessions list_sessions = 12;
  }

  message UserMessage {
    // repeated ContentBlock, not a bare string — see "UserMessage carries
    // ContentBlocks" below.
    repeated pluggableharness.agent.content.v1.ContentBlock content = 2;
  }

  message SlashCommand {
    string name = 1;   // without the leading slash
    string args = 2;   // raw argument string
  }

  message PlanDecision {
    string plan_item_id = 1;
    ClientDecision decision = 2;
    optional google.protobuf.Struct corrected_input = 3;
    PlanDecisionScope scope = 4;
  }

  message InteractiveResponse {
    string call_id = 1;
    google.protobuf.Struct response = 2;
  }

  message ActionTrigger {
    string node_id = 1;
    string tool_name = 2;
    google.protobuf.Struct args = 3;
    string provider = 4;
  }

  message Interrupt {}
}
```

`ActionTrigger` is what a frontend dispatches when the operator activates a [`RenderTree`'s `ActionNode`](render-tree.md#interactive-content-the-action-node); `node_id`, `tool_name`, `args`, and `provider` are echoed unchanged from the originating node — `provider` disambiguates which provider's operation to invoke, since `tool_name` is only unique per provider. The kernel handles the resulting `action_trigger` identically to a `direct_invoke` slash command (below): the normal `Invoke`/plan-apply pipeline, including policy evaluation, with no model turn.

### UserMessage carries ContentBlocks

`ClientEvent.UserMessage` no longer carries a bare `string text`; it carries `repeated content.v1.ContentBlock content`, the same block vocabulary [`model/data-types.md#canonical-message--content-block-schema`](../model/data-types.md#canonical-message--content-block-schema) uses everywhere else in this protocol series. The prior string-only shape had no entry point for a pasted image: `content.v1.ImageBlock` and a model's `supports_vision` capability flag already existed, but a frontend had no way to actually *submit* one as user input. A frontend sending ordinary typed text sends a single `TextBlock`; a frontend supporting paste/attachment sends `content` with more than one block (e.g. a `TextBlock` plus an `ImageBlock`), gated the same way any other `ImageBlock` is — the kernel MUST reject an image block against a model whose `ModelSpec.supports_vision` is false, per [`model/data-types.md`](../model/data-types.md). Field 1 (the old `text` field) is `reserved`, per `.claude/rules/proto.md`'s field-number discipline — never reused.

### `plan_decision.corrected_input`

`PlanDecision.corrected_input` is an opencode-style `CorrectedError` redirect: rather than a binary allow/deny, the operator can supply corrected tool arguments and have the kernel treat the item as allowed with those arguments instead of the model's originals. When present, the kernel **MUST** re-validate `corrected_input` against the tool's `input_schema` ([`tool/data-types.md#toolschema`](../tool/data-types.md#toolschema)) before treating the item as allowed — an invalid correction **MUST** be rejected back to the sending frontend as a distinct error, never silently coerced and never silently downgraded to a plain `deny`. This re-validation is mechanically part of the plan/apply gate's decision handling; see [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md) for where it slots into the turn algorithm — this document defines the field and the frontend-facing contract around it, the gate owns applying it.

`PlanDecision.scope` is orthogonal to `decision`/`corrected_input`: it says how durably this verdict is remembered, not what the verdict is. `PLAN_DECISION_SCOPE_ONCE` (the default a frontend SHOULD send absent explicit operator intent) applies only to the named item; `SESSION` applies to the rest of the current session for matching calls; `ALWAYS` asks the kernel to persist the verdict as policy, outliving the session. See [`agent-loop/plan-apply-gate.md#plandecisionscope-semantics`](../agent-loop/plan-apply-gate.md#plandecisionscope-semantics) for evaluation order and the `ALWAYS` persistence obligation — this document defines the field, the gate owns applying it, exactly as with `corrected_input` above.

## Session lifecycle

A frontend's single `Attach` stream subscribes and unsubscribes individual sessions via six control `ClientEvent` variants, correlated to their `ServerEvent` responses by a client-generated `request_id` the frontend chooses and the kernel echoes back unchanged on `ServerEvent.request_id`:

| `ClientEvent` variant | Answered by | Purpose |
|---|---|---|
| `hello` | — (no response) | MAY be sent first, on stream open; asserts `protocol_version` only. Does not bind any session — attaching happens via the variants below. |
| `create_session` | `session_created` | Creates a new session under a given (or default) profile and working directory, optionally seeded with an `initial_prompt`. Auto-attaches the sending stream to the new session. |
| `attach_session` | `session_attached`, then a backfill batch, then `backfill_complete` | Subscribes an existing session (live or terminal) onto this stream. |
| `resume_session` | Same shape as `attach_session` | Attaches a historical session — see [Resume and re-open semantics](#resume-and-re-open-semantics) below for what "resume" permits beyond a plain attach. |
| `detach_session` | `session_detached` | Unsubscribes a session from this stream. Does not affect the session itself, or any other stream still attached to it. |
| `list_sessions` | `session_list` | The one connection-level (not session-scoped) control event — an optional `status`/`parent_session_id` filter and a `roots_only` flag. |

`CreateSession`, `AttachSession`, `ResumeSession`, `DetachSession`, and `ListSessions` all carry their own `request_id`; `AttachSession`/`ResumeSession`/`DetachSession` additionally carry the target `session_id` in their own body (the top-level `ClientEvent.session_id` is empty for all six control variants, since none of them yet has — or, for `list_sessions`, ever has — a session to scope to before the response arrives).

### Backfill = the replay path, not a new subsystem

`architecture.md`'s "replay is just feed old events through the same Render/Paint path" governs `attach_session`/`resume_session` identically to any other replay: on either, the kernel replays the session's persisted events in `sequence` order, re-rendering each via the "supersedes" model (the historical `ProducerRef`'s own `Render`), and emits them as ordinary `ServerEvent.render` — the identical wire shape a live render uses. `stream_delta` is live-only; replayed text arrives as finished `render`s, never as deltas.

The batch is bracketed: `SessionAttached` (carrying the session's current `SessionInfo`) opens it, the replayed `render` events follow, and `BackfillComplete { last_sequence }` closes it — the done-marker a frontend uses to know it has caught up, after which live events (`sequence > last_sequence`) continue on the same stream. **Backfill is unicast to the attaching stream only, never broadcast** to other frontends already subscribed to that session — a late joiner catching up must not re-flood everyone who was already there.

### Session scope

**Multiple frontends MAY subscribe to the same session concurrently**, each on its own `Attach` stream — see [`README.md#session-scope--multi-attach`](README.md#session-scope--multi-attach) for why this is the current design, not a 1:1 attachment model. The operational rules, all re-scoped **per session** now that one connection's stream carries many sessions at once:

- **`ServerEvent`s for a given session broadcast identically to every frontend subscribed to that session.** No partitioning, no "primary" frontend — every subscribed frontend observes that session's live stream in the same order. A frontend subscribed to session A does not receive session B's events at all, regardless of how many sessions each of its peers is subscribed to.
- **`ClientEvent`s are processed in kernel arrival order, per session.** `user_message`/`slash_command`/`action_trigger`/`interrupt` have no real conflict — multiple frontends sending these for the same session just interleave as ordinary sequential input, a legitimate pairing/multi-operator scenario, not an error case.
- **`plan_decision`/`interactive_response` name a specific pending item** (`plan_item_id`/`call_id`), implicitly scoped to the session that item belongs to. **First response for a given item wins.** Any subsequent response for an already-resolved item **MUST** be rejected back to the sending frontend with a distinct `invalid_client_event`-category error (see [Error taxonomy](#error-taxonomy)), never silently dropped and never silently re-applied — so that frontend's UI can show "already decided elsewhere" rather than appearing to hang.
- Connection-level control responses (`session_list`) are naturally exempt from all of the above — they answer the one requesting stream directly, correlated by `request_id`, and are never broadcast to any other frontend.

### Resume and re-open semantics

`ResumeSession` targets a **historical** session — one that may be RUNNING (an ordinary late-attach, identical to `AttachSession`), or may already be terminal. What a terminal session's resume permits depends on which terminal status it's in:

- **`SESSION_STATUS_COMPLETED` or `SESSION_STATUS_CANCELLED`** MAY be re-opened to `SESSION_STATUS_RUNNING` for new turns. A subsequent `user_message` against a re-opened session is accepted and starts a fresh turn; the session's bounds (`max_turns`, `max_budget_usd`, `max_wall_clock`) reset fresh from its originating profile — a resumed session does not inherit whatever fraction of its bounds it had already consumed before reaching its prior terminal status. The kernel MUST record this `status` transition (terminal → `RUNNING`) the same way any other status change is recorded in `session_meta` (`state-backend.md`'s "Schema migration & corruption recovery" notwithstanding — this is an ordinary in-place status update, not a schema change).
- **A bound-exhausted status (`SESSION_STATUS_ERROR_MAX_TURNS`, `SESSION_STATUS_ERROR_MAX_BUDGET_USD`, `SESSION_STATUS_ERROR_MAX_WALL_CLOCK`) or `SESSION_STATUS_FAILED`** is **replay-only** in this protocol revision. `ResumeSession` against one of these still succeeds — the kernel attaches the stream and backfills its full history exactly as for any other session — but the kernel **MUST** reject any subsequent `user_message` (or any other new-turn-inducing event) against it with `FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY`, a category distinct from `SESSION_BUSY` precisely because the session isn't running — it's terminal and specifically barred from new turns, not merely occupied.

A plain `AttachSession` (as opposed to `ResumeSession`) against a terminal session is a read-only backfill-and-watch — it never implicitly re-opens anything; only `ResumeSession` carries re-open intent, and even then only for the two statuses above.

### No session deletion

**No `DeleteSession` (or any other deletion mechanism) exists anywhere in this protocol, for any plugin category, including frontends.** This is a deliberate omission, not a gap: sessions are protected from every plugin, per `state-backend.md`'s retention posture ("no implicit expiry; pruning is an explicit operator action") — pruning a session file is a kernel CLI command an operator runs at the keyboard, never something a frontend (or any other plugin) can trigger over the wire. `ServerEvent`'s field 13 — where a `session_deleted` variant would otherwise have gone, mirroring `session_created`/`session_attached`/`session_detached`'s numbering — is `reserved`, not assigned, specifically so that number is never silently repurposed for something unrelated if frontend-triggered deletion is ever reconsidered by a future protocol revision.

## Slash commands

`SlashCommandSpec` is defined once, canonically, here — every other category's capability response ([`model/protocol.md#getcapabilities`](../model/protocol.md#getcapabilities), [`tool/protocol.md#getschema`](../tool/protocol.md#getschema), and the equivalent sections in `context/` and `memory/`) declares an optional `[]SlashCommandSpec` field of this same type and links back here rather than redefining it. The wire type is factored out into its own shared vocabulary for exactly that reason: it's shared by every provider category's `GetCapabilities`/`GetSchema` response, not owned by the frontend category alone.

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

A name collision across providers **MUST** be a config-load-time error, per this protocol series' established "ambiguity is an error, not a silent pick" pattern. The kernel aggregates every loaded provider's declared commands into one profile-scoped `SlashCommandRegistry`, sent to an attaching frontend as part of `session_attached` and again whenever the registry changes (a plugin reload, a config change) — a frontend does not need to separately call every category's `GetCapabilities`/`GetSchema` and merge the results itself.

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
  FRONTEND_ERROR_CATEGORY_SESSION_NOT_FOUND = 5;
  FRONTEND_ERROR_CATEGORY_SESSION_CREATE_FAILED = 6;
  FRONTEND_ERROR_CATEGORY_SESSION_BUSY = 7;
  FRONTEND_ERROR_CATEGORY_SCHEMA_TOO_NEW = 8;
  FRONTEND_ERROR_CATEGORY_SESSION_REPLAY_ONLY = 9;
}

message FrontendError {
  FrontendErrorCategory category = 1;
  string message = 2;
}
```

| Category | Meaning | Requirement |
|---|---|---|
| `render_failed` | A specific `RenderTree` node couldn't be painted (e.g. a malformed `diff`). | MUST fall back to a generic text rendering of whatever content is recoverable; MUST NOT crash the frontend process over one bad node. |
| `invalid_client_event` | Malformed input on the operator-facing side — including a `plan_decision`/`interactive_response` naming an already-resolved item (see [Session scope](#session-scope)), or a session-scoped variant arriving with an empty `session_id`. | Rare in the ordinary case, since the frontend itself constructs `ClientEvent`s; MUST be surfaced distinctly, not collapsed into `unknown`. |
| `region_unsupported` | A producer targeted a `Region` this frontend has no fallback behavior for at all. | SHOULD be logged; MUST NOT be treated as fatal. |
| `session_not_found` | `attach_session`, `resume_session`, `detach_session`, or `list_sessions`' `parent_session_id` filter named a `session_id` the kernel has no record of. | MUST be surfaced distinctly, correlated by `request_id`. |
| `session_create_failed` | `create_session` failed — an invalid profile, or an unusable working directory. | MUST be surfaced distinctly, correlated by `request_id`. |
| `session_busy` | Reserved for a future session-mutating control event that conflicts with a `RUNNING` session. No variant in this protocol revision currently triggers it. | — |
| `schema_too_new` | `resume_session` named a session file with a `PRAGMA user_version` newer than this kernel understands (`state-backend.md`'s "Schema migration"). | MUST be surfaced distinctly; the kernel MUST refuse to open the file, per `state-backend.md`. |
| `session_replay_only` | A new-turn-inducing event (`user_message`, ...) targeted a session attached replay-only — see [Resume and re-open semantics](#resume-and-re-open-semantics). | MUST be surfaced distinctly, never silently ignored. |
| `unknown` | Anything else. | Structured error taxonomy applies here as everywhere else in this protocol series — see [`conformance.md`](conformance.md#error-taxonomy). |

`ConfigureResponse` errors surface as a gRPC status carrying a `FrontendError` in its structured detail, not as an in-band field on the response message. Errors encountered mid-`Attach` surface as `ServerEvent.error`, carrying `request_id` when they answer a specific control event.
