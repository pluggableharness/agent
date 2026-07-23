# Frontend & widget provider protocols

Covers **two** plugin categories in one directory, both concerned with what the operator sees and does, neither owning the agent loop itself:

- **Frontend provider** ([`frontend-protocol.md`](frontend-protocol.md)) — owns the terminal (or window, or voice channel): the process the kernel's state-event stream attaches to, responsible for actually painting pixels/text and turning operator input into `ClientEvent`s.
- **Widget provider** ([`widget-protocol.md`](widget-protocol.md)) — contributes content *into* whichever frontend is active, without owning it. This is a genuine sixth plugin category, not merely an extension of the other five (see [`architecture.md`](../architecture.md#the-six-provider-categories)) — a git-status panel or a context-budget indicator isn't naturally "a tool" or "a context provider," it just wants to put something on screen.

Both categories share one vocabulary, formalized once and reused verbatim: the [`RenderTree`](render-tree.md#rendertree) intermediate representation (text runs, code blocks, diffs, tables, links, a sub-session node, an interactive `action` node) and the region/placement model ([`render-tree.md`](render-tree.md#placement--regions)). Every other plugin category's optional `Render` RPC — [`provider/protocol.md#render`](../provider/protocol.md#render), [`tool/protocol.md#render`](../tool/protocol.md#render), and the equivalent sections in `context/` and `memory/` — returns exactly this type; this directory is where the type itself is formally, canonically defined.

The wire protocol for both categories, plus the shared `RenderTree` IR and the `SlashCommandSpec` type, is defined as gRPC services with protobuf messages — see [`examples.md`](examples.md) for the schema. `RenderTree` and `SlashCommandSpec` are deliberately factored into their own shared vocabulary rather than nested inside the frontend or widget definitions: both the frontend and widget protocols place content into the same `Region` vocabulary, and every provider category (not just frontend/widget) declares a `SlashCommandSpec` in its capability response — see [`frontend-protocol.md#slash-commands`](frontend-protocol.md#slash-commands).

## Transport & lifecycle

Subprocess + gRPC via `hashicorp/go-plugin`, per [`architecture.md`](../architecture.md#transport). Standard handshake (magic cookie, protocol version negotiation) applies uniformly across all six provider categories and isn't repeated per category.

The two categories' primary RPC has **different streaming shapes**, and this is the one thing about this directory most worth getting right, since it's easy to conflate the two:

- A **frontend** provider plugin exposes `GetCapabilities`, `Configure`, `Attach`. `Attach` is **genuinely bidirectional** — the frontend sends `ClientEvent`s (operator input) and receives `ServerEvent`s (kernel state) on the same live stream, because the operator can type a message while prior content is still rendering. This is one of only two truly bidirectional RPCs anywhere in this protocol series, the other being the kernel-callback channel; every other category's primary RPC (`StreamCompletion`, `Invoke`) is server-streaming-plus-cancellation. See [`frontend-protocol.md#transport`](frontend-protocol.md#transport).
- A **widget** provider plugin exposes `GetCapabilities`, `Configure`, `Attach` too — same RPC name, **different shape**: widget `Attach` is **server-streaming only**. A widget is passive/display-only in v1; it never sends anything back over this channel. A widget wanting to trigger an action does so by also being a tool provider with a slash command, not through `Attach`. See [`widget-protocol.md#transport`](widget-protocol.md#transport).

Both plugins MAY additionally implement `Render`, per the general Emit→Render→Paint pipeline ([`architecture.md`](../architecture.md#emit--render--paint-pipeline)) — though in practice a frontend/widget is far more often a `Render` *consumer* (painting other categories' trees) than a producer of its own.

## Session scope — multi-attach

**Multiple frontends MAY `Attach` to the same session concurrently** — a TUI and a web tail both watching one live session, for example. This follows naturally from how widgets already work: any number of panels can subscribe to the same hook stream, so frontends support the same multiplicity rather than being constrained to a single attachment.

- **`ServerEvent`s broadcast identically to every attached frontend.** No partitioning, no "primary" frontend — every attached frontend sees the same live stream, in the same order.
- **`ClientEvent`s are processed in kernel arrival order**, with one specific arbitration rule for decisions that can only be honored once. See [`frontend-protocol.md#session-scope`](frontend-protocol.md#session-scope) for the full rule (first-response-wins on `plan_decision`/ `interactive_response`, with a distinct rejection error for a late-arriving second response).

## Category structure

- [`render-tree.md`](render-tree.md) — the `RenderTree` IR itself: every node type, the placement/region vocabulary, and why both categories share one definition. The canonical reference every other category's `Render` points back to.
- [`frontend-protocol.md`](frontend-protocol.md) — the frontend provider protocol: transport, fast-path text deltas vs. full `Render`, session scope/multi-attach, slash commands (`SlashCommandSpec`, defined once, canonically, here), the `plan_decision.corrected_input` redirect, and the error taxonomy for this category.
- [`widget-protocol.md`](widget-protocol.md) — the widget provider protocol: transport (server-streaming, not bidi), deriving display state from `observe`-mode hooks with no new data feed, and interactive widgets via the `action` `RenderNode`.
- [`examples.md`](examples.md) — wire-protocol excerpts for all three schemas, a worked frontend `Attach` sequence (plan-ready → render → approve/reject/edit), and a worked widget example (a status-bar widget deriving state from the same event stream a frontend sees).
- [`conformance.md`](conformance.md) — the error taxonomy, the MUST/SHOULD/ MAY summary matrix for both categories, and any genuinely open questions.
