# Frontend & widget — conformance

## Error taxonomy

The frontend provider category has a structured error type, `FrontendError`:

| Category | Meaning | Requirement |
|---|---|---|
| `render_failed` | A specific `RenderTree` node couldn't be painted (e.g. a malformed `diff`). | MUST fall back to a generic text rendering of whatever content is recoverable; MUST NOT crash the frontend process over one bad node. |
| `invalid_client_event` | Malformed input on the operator-facing side, including a `plan_decision`/`interactive_response` naming an already-resolved item. | MUST be surfaced distinctly, not collapsed into `unknown` — rare in the ordinary case since the frontend itself constructs `ClientEvent`s. |
| `region_unsupported` | A producer targeted a `Region` this frontend has no fallback behavior for at all. | SHOULD be logged; MUST NOT be treated as fatal. |
| `unknown` | Anything else. | MUST include enough detail for debugging; treated as non-retryable by default. |

A `Configure`-time `FrontendError` surfaces as a gRPC status carrying the error in its structured detail, mapped to `codes.InvalidArgument` for a malformed config value and `codes.Internal` for anything unmapped — the same canonical "specific code, never a bare `codes.Unknown`" discipline every category in this protocol series follows. An error encountered mid-`Attach` (a bad render, an invalid client event) surfaces in-band as `ServerEvent.error` instead, since `Attach` is a long-lived stream where tearing down the whole connection over one recoverable error would be far too disruptive — only a genuinely fatal condition (the plugin process itself failing) closes the stream with a gRPC status.

**The widget provider category has its own structured error type, `WidgetError`** ([`widget-protocol.md#error-taxonomy`](widget-protocol.md#error-taxonomy)), mirroring `FrontendError`'s category/message shape. Unlike the frontend category, widget `Attach` has no in-band return channel — it's server-streaming only — so `WidgetError` is always carried in the structured detail of a gRPC status on `Configure` or `Attach`, mapped per the same canonical table (`codes.InvalidArgument`/`codes.Internal`/`codes.Canceled`), never as an in-band stream message. This resolves what was previously an open question in this document about whether widgets needed a categorized error channel at all — they do, for the same reason a partial-failure condition (e.g. "this update renders for one region but not another") needs a name a widget author can report distinctly from an unstructured `codes.Internal`.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `RenderTree` node types render gracefully, including unknown/unspecialized ones | MUST | [`render-tree.md`](render-tree.md#rendertree) |
| Placement is a hint, never a mandate | MUST (frontend may reinterpret) | [`render-tree.md`](render-tree.md#placement--regions) |
| `overlay` visually distinct from ambient content | MUST | [`render-tree.md`](render-tree.md#placement--regions) |
| `Render` payloads carry a `schema_version`, echoed unchanged from emit time | MUST | [`render-tree.md`](render-tree.md#schema-versioning) |
| Frontend `Attach` is bidirectional and connection-scoped, multiplexing every subscribed session | MUST | [`frontend-protocol.md`](frontend-protocol.md#transport) |
| `Describe` reports this plugin build's own identity from the running process | MUST | [`frontend-protocol.md`](frontend-protocol.md#transport), [`widget-protocol.md`](widget-protocol.md#transport) |
| Streaming text via fast-path deltas, not per-token `Render` | MUST | [`frontend-protocol.md`](frontend-protocol.md#fast-path-vs-full-render) |
| `UserMessage` carries `repeated ContentBlock`, not a bare string | MUST | [`frontend-protocol.md`](frontend-protocol.md#usermessage-carries-contentblocks) |
| `plan_decision.corrected_input` re-validated against `input_schema` | MUST | [`frontend-protocol.md`](frontend-protocol.md#plan_decisioncorrected_input) |
| `plan_decision.scope` (`ONCE`/`SESSION`/`ALWAYS`) governs how durably a decision applies | MUST | [`frontend-protocol.md`](frontend-protocol.md#plan_decisioncorrected_input), [`agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md#plandecisionscope-semantics) |
| `interactive_request`/`interactive_response` for `kind: interactive` tool calls | MUST | [`frontend-protocol.md`](frontend-protocol.md#fast-path-vs-full-render) |
| Session lifecycle (create/attach/resume/detach/list) over `ClientEvent`/`ServerEvent` control variants, correlated by `request_id` | MUST | [`frontend-protocol.md`](frontend-protocol.md#session-lifecycle) |
| Backfill on attach is a bracketed, unicast replay batch, never broadcast | MUST | [`frontend-protocol.md`](frontend-protocol.md#backfill--the-replay-path-not-a-new-subsystem) |
| A COMPLETED/CANCELLED session MAY be re-opened to RUNNING via `ResumeSession`; bound-exhausted/FAILED sessions are replay-only | MUST | [`frontend-protocol.md`](frontend-protocol.md#resume-and-re-open-semantics) |
| No session deletion mechanism exists for any plugin category | MUST (by decision) | [`frontend-protocol.md`](frontend-protocol.md#no-session-deletion) |
| Multiple frontends MAY subscribe to the same session concurrently | MAY | [`README.md`](README.md#session-scope--multi-attach) |
| `ServerEvent`s broadcast identically to every frontend subscribed to a given session | MUST | [`frontend-protocol.md`](frontend-protocol.md#session-scope) |
| First-response-wins on `plan_decision`/`interactive_response`, per session; losers get a distinct error | MUST | [`frontend-protocol.md`](frontend-protocol.md#session-scope) |
| Widget `Attach` is server-streaming only (no bidi channel), and remains session-scoped, not connection-multiplexed | MUST | [`widget-protocol.md`](widget-protocol.md#transport) |
| Widget-initiated actions via `ActionNode` + frontend `action_trigger`, not a widget-specific RPC | MUST | [`widget-protocol.md`](widget-protocol.md#interactive-widgets) |
| Widgets derive state via `observe`-mode hooks, no separate data feed | MUST | [`widget-protocol.md`](widget-protocol.md#deriving-display-state--no-new-data-feed) |
| `slash_commands` declarable by any provider category | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| Slash-command name collision across providers | MUST be config-load-time error | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| Aggregate `SlashCommandRegistry` sent on session attach and on registry change | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| `direct_invoke` dispatch bypasses the model turn | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| `prompt_expansion` dispatch costs a model turn | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| `prompt_expansion` scoping via `agent_profile.slash_commands` | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands), [`configuration/agent-profiles.md`](../configuration/agent-profiles.md) |
| `ActionNode`/`ActionTrigger` carry `provider`, since `tool_name` is only unique per provider | MUST | [`render-tree.md`](render-tree.md#interactive-content-the-action-node) |
| `ActionNode` dispatches through the same path as `direct_invoke` | MUST | [`render-tree.md`](render-tree.md#interactive-content-the-action-node) |
| Reference TUI's region layout | Not normative (one implementation) | [`examples.md`](examples.md#the-reference-tui) |
| Structured `FrontendError` taxonomy | MUST | [Error taxonomy](#error-taxonomy) |
| Structured `WidgetError` taxonomy | MUST | [Error taxonomy](#error-taxonomy) |

## Open questions

- **Whether `region_unsupported` should distinguish "frontend has no concept of this region at all" from "frontend recognizes the region but is out of space for it."** Both currently collapse to the same category; a widget author tuning `priority` might want to tell them apart. `supported_regions`/`supported_hook_points` capability advertisement (this revision) narrows this somewhat — a producer can now check `supported_regions` proactively — but doesn't fully resolve the reactive-error case.
- **Whether a future non-TUI frontend (web, voice) needs additional `Region` values**, or whether the existing six adequately cover every conforming implementation's actual layout needs — the abstract vocabulary was designed against one reference implementation (the TUI) plus a thought experiment (a hypothetical web/voice frontend), not a second built frontend to validate against.
- **`FRONTEND_ERROR_CATEGORY_SESSION_BUSY` currently has no triggering variant** in this protocol revision (no frontend-triggered session-mutating control event conflicts with a `RUNNING` session — `DetachSession` is always safe). It stays reserved for a future control event that would need it, rather than being removed, since a category enum value once shipped is never renumbered per `.claude/rules/proto.md`.
