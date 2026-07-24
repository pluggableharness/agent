// Package hook is the plugin-author-facing SDK for
// pluggableharness.hook.v1.HookSubscriberService, the wire contract
// described in full in docs/specifications/agent-loop/hook-dispatch.md
// (mechanics) and docs/specifications/architecture.md's "Hook dispatch
// semantics" section (the surrounding observe/transform/veto model).
//
// # A shared service, not a category SDK
//
// Unlike pkg/model, pkg/tool, pkg/context, pkg/memory, pkg/frontend, and
// pkg/widget, this package is not tied to one of the seven plugin
// categories. HookSubscriberService is a service any category plugin MAY
// additionally implement alongside its primary category service, muxed on
// the same hashicorp/go-plugin subprocess connection via
// pkg/plugin.Config.Services (hook-dispatch.md's "one shared service, not
// a per-category RPC"). A tool plugin that also wants to observe
// post-model-response, or a model plugin that wants to veto plan-ready,
// builds a Subscriber (or one of its narrower Observer/Transformer/Vetoer
// facets) and passes NewService's result alongside its category Service in
// the same Config.Services slice — this package has no dependency on any
// of the six category SDKs and no category SDK needs to depend on it to be
// usable standalone.
//
// # Eight of nine hook points
//
// common.v1.HookPoint enumerates eight of architecture.md's nine named
// hook points; context-assemble is deliberately absent — it stays on
// ContextService.Contribute (docs/specifications/context/protocol.md#contribute-the-context-assemble-rpc),
// which already carries the full accumulated ContextSection chain, rather
// than riding this generic surface a second time. hook.go documents the
// eight points this package does serve.
//
// # Dispatch modes and the split Subscriber interfaces
//
// DispatchHook is unary: one hook-point firing, delivered to one
// subscriber, is one request/one response
// (hook-dispatch.md#dispatch-order-and-payload-flow). The kernel-side
// dispatch loop that walks the ordered chain of subscribers for a given
// hook point, and that decides what an observe-mode failure or a
// veto-mode timeout means for the *rest* of that chain, lives in the
// kernel, not here — this package's Service adapts exactly one
// subscriber's exactly one RPC invocation. See server.go's DispatchHook
// for what that boundary means concretely for observe-mode error
// handling.
//
// A plugin author implements one or more of Observer, Transformer, and
// Vetoer (hook.go) — not a single monolithic Subscriber interface —
// because a real subscription is declared per (hook point, mode) pair in
// agent.hcl, and most plugins subscribe to only one or two combinations.
// NewService detects which of the three a given value implements via type
// assertion, the same optional-facet pattern pkg/render's Render/Preview
// split uses, so a pure audit logger never has to write no-op Transform
// and Veto stubs it will never be called for.
package hook
