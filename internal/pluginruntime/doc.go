// Package pluginruntime is the kernel-side launcher for one of the seven
// out-of-process PluggableHarness Agent plugin categories (provider, tool, context,
// memory, frontend, widget, slashcommand), each speaking gRPC over
// github.com/hashicorp/go-plugin.
//
// Launch runs the full launch sequence — pre-flight version check,
// subprocess spawn under a minimal environment allowlist, handshake, the
// authoritative post-handshake protocol-version gate, and dispense — and
// returns a *Plugin whose Dispensed() is the raw generated category
// service client (modelv1.ModelServiceClient, toolv1.ToolServiceClient,
// etc.). Every launched plugin is simultaneously wired with a real,
// servable KernelCallbackService on a fixed, well-known broker ID
// (pkg/common.CallbackBrokerID), so the plugin can call back into the
// kernel from the moment it starts.
//
// Plugin.HookClient returns a hookv1.HookSubscriberServiceClient dialed
// over the same muxed connection the category client came from, which is
// what specifications/agent-loop/hook-dispatch.md requires of hook
// dispatch — go-plugin carries several gRPC services over one subprocess
// connection, and this package hands out exactly that one extra client
// rather than the raw connection it owns.
//
// See README.md for the package's role in the wider system and
// CLAUDE.md for implementation-level conventions and gotchas
// (specifications/plugin-runtime.md and
// specifications/kernel-callbacks.md are the governing specs).
package pluginruntime
