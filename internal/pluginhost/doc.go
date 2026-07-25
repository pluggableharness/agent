// Package pluginhost launches, configures, registers, and tears down
// every plugin subprocess a session needs, and owns the live registry
// the rest of the kernel looks providers up in.
//
// It is the consumer of internal/providerresolve: given that package's
// ordered list of resolved binaries, Supervisor.Start runs the full
// per-provider bring-up sequence — launch, Describe, checksum verify,
// schema fetch, config decode, Configure, register — in order, and
// all-or-nothing. Supervisor.Shutdown reverses it.
//
// Three properties are load-bearing:
//
//   - Order. Providers launch in providerresolve.Order's sequence,
//     which is agent.hcl declaration order. That is hook-dispatch order
//     (configuration/agent-profiles.md), and its reverse is shutdown
//     order (dependency-reverse teardown).
//
//   - Atomicity. A failure at any step of any provider's sequence tears
//     down every provider already launched, in reverse, before Start
//     returns. A half-started kernel is never handed to a session.
//
//   - Late-bound callback identity. A plugin's own kernel-callback
//     server has to exist before the subprocess is launched, but the
//     identity and resolved config that server answers with are only
//     known after Describe and after the config decode that Describe's
//     schema makes possible. callbackSlot resolves that ordering
//     problem, so a plugin calling GetConfig or Log from inside its own
//     Configure handler — which kernel-callbacks.md permits — sees the
//     right answers.
//
// Registry is the read side and is safe for concurrent use; Supervisor
// is the write side and is driven by one goroutine.
package pluginhost
