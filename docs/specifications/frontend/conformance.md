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

**The widget provider category has no structured error type of its own.** There is no `WidgetError` message — widget failures surface only as ordinary gRPC status codes on `Configure` or `Attach` (mapped per the same canonical table: `codes.InvalidArgument`/`codes.Internal`/`codes.Canceled` as appropriate), with no in-band structured category the way `FrontendError` provides for the frontend category. Whether this is a deliberate scope reduction (a widget is passive/display-only, so there's less to classify) is an open question below.

## Required vs. optional support — summary matrix

| Capability | Level | Notes |
|---|---|---|
| `RenderTree` node types render gracefully, including unknown/unspecialized ones | MUST | [`render-tree.md`](render-tree.md#rendertree) |
| Placement is a hint, never a mandate | MUST (frontend may reinterpret) | [`render-tree.md`](render-tree.md#placement--regions) |
| `overlay` visually distinct from ambient content | MUST | [`render-tree.md`](render-tree.md#placement--regions) |
| Frontend `Attach` is bidirectional | MUST | [`frontend-protocol.md`](frontend-protocol.md#transport) |
| Streaming text via fast-path deltas, not per-token `Render` | MUST | [`frontend-protocol.md`](frontend-protocol.md#fast-path-vs-full-render) |
| `plan_decision.corrected_input` re-validated against `input_schema` | MUST | [`frontend-protocol.md`](frontend-protocol.md#plan_decisioncorrected_input) |
| `interactive_request`/`interactive_response` for `kind: interactive` tool calls | MUST | [`frontend-protocol.md`](frontend-protocol.md#fast-path-vs-full-render) |
| Multiple frontends MAY `Attach` concurrently | MAY | [`README.md`](README.md#session-scope--multi-attach) |
| `ServerEvent`s broadcast identically to every attached frontend, when multi-attach occurs | MUST | [`frontend-protocol.md`](frontend-protocol.md#session-scope) |
| First-response-wins on `plan_decision`/`interactive_response`; losers get a distinct error | MUST | [`frontend-protocol.md`](frontend-protocol.md#session-scope) |
| Widget `Attach` is server-streaming only (no bidi channel) | MUST | [`widget-protocol.md`](widget-protocol.md#transport) |
| Widget-initiated actions via `ActionNode` + frontend `action_trigger`, not a widget-specific RPC | MUST | [`widget-protocol.md`](widget-protocol.md#interactive-widgets) |
| Widgets derive state via `observe`-mode hooks, no separate data feed | MUST | [`widget-protocol.md`](widget-protocol.md#deriving-display-state--no-new-data-feed) |
| `slash_commands` declarable by any provider category | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| Slash-command name collision across providers | MUST be config-load-time error | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| `direct_invoke` dispatch bypasses the model turn | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| `prompt_expansion` dispatch costs a model turn | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands) |
| `prompt_expansion` scoping via `agent_profile.slash_commands` | MUST | [`frontend-protocol.md`](frontend-protocol.md#slash-commands), [`configuration/agent-profiles.md`](../configuration/agent-profiles.md) |
| `ActionNode` dispatches through the same path as `direct_invoke` | MUST | [`render-tree.md`](render-tree.md#interactive-content-the-action-node) |
| Reference TUI's region layout | Not normative (one implementation) | [`examples.md`](examples.md#the-reference-tui) |
| Structured `FrontendError` taxonomy | MUST | [Error taxonomy](#error-taxonomy) |
| Structured `WidgetError` taxonomy | Not currently specified — see [Open questions](#open-questions) | [Error taxonomy](#error-taxonomy) |

## Open questions

- **Whether the widget provider category needs its own structured error type.** `FrontendError` gives the frontend category a categorized, in-band error channel; the widget protocol as built has no equivalent — widget failures are only ordinary gRPC status codes. Unclear whether that's an intentional consequence of widgets being passive/display-only (less to classify: a widget either renders or its stream errors out) or simply an unspecified gap. Worth resolving before a third-party widget author needs to decide how to report a partial-failure condition (e.g. "this widget can display some but not all of its regions").
- **Whether `region_unsupported` should distinguish "frontend has no concept of this region at all" from "frontend recognizes the region but is out of space for it."** Both currently collapse to the same category; a widget author tuning `priority` might want to tell them apart.
- **Whether a future non-TUI frontend (web, voice) needs additional `Region` values**, or whether the existing six adequately cover every conforming implementation's actual layout needs — the abstract vocabulary was designed against one reference implementation (the TUI) plus a thought experiment (a hypothetical web/voice frontend), not a second built frontend to validate against.
