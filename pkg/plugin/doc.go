// Package plugin is the shared serving layer every PluggableHarness Agent
// plugin-category SDK (pkg/model, pkg/tool, pkg/context, pkg/memory,
// pkg/frontend, pkg/widget, pkg/slashcommand) is built on to run a plugin
// subprocess. A plugin author's main() constructs a Config — their own
// Identity, the pluggableharness.common.v1.Category they implement, a
// *Callback, and one or more Services — and calls Serve, which blocks for
// the life of the process.
//
// A category SDK's own server.go returns a Service wrapping that
// category's generated <X>ServiceServer; a plugin author passes more than
// one Service to Config.Services to mux additional service surfaces onto
// the same subprocess connection — most commonly hook.v1.HookSubscriberService
// (docs/specifications/agent-loop/hook-dispatch.md) and, for a tool,
// frontend widget, or slashcommand provider that wants a direct-invoke
// shortcut, slashcommand.v1.SlashCommandService
// (docs/specifications/slashcommand/data-types.md). This multi-service
// muxing on one subprocess connection is spec-mandated, not optional — see
// also docs/specifications/tool/protocol.md#getschema and
// docs/specifications/frontend/widget-protocol.md#transport — and is
// exactly what hashicorp/go-plugin's GRPCServer(broker, *grpc.Server)
// hook exists to support: registering more than one gRPC service on the
// single *grpc.Server the subprocess serves
// (.claude/rules/plugin-runtime.md).
//
// # The callback-timing trap
//
// Every plugin subprocess is handed a channel back to
// KernelCallbackService (docs/specifications/kernel-callbacks.md) at a
// fixed, well-known broker ID (pkg/common.CallbackBrokerID) — but the
// kernel does not start serving that channel until it dispenses this
// plugin's client, which happens only after this package's internal
// GRPCServer method has already returned
// (.claude/rules/plugin-runtime.md's "Handshake" section describes the
// same handshake sequence from the kernel side). Dialing the broker
// synchronously inside GRPCServer therefore races, and typically loses,
// against the kernel's own dispense step.
//
// Callback exists specifically to route around this. NewCallback returns
// a handle that is not yet connected to anything; Serve's internal
// plugin.GRPCPlugin adapter records the broker on that handle once
// GRPCServer runs, but does not dial it. The actual broker.Dial only
// happens the first time a plugin author's own RPC handler — running well
// after go-plugin has finished dispensing this process's client back to
// the kernel — calls Callback.Client. Do not "fix" this laziness by
// dialing eagerly inside GRPCServer, a constructor, or any other
// call that can run before this plugin's own gRPC handlers start serving
// traffic; that reintroduces the exact deadlock this design exists to
// avoid.
package plugin
