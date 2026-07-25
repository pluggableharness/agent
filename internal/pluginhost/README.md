# internal/pluginhost

Launches, configures, registers, and tears down every plugin subprocess a
session needs, and owns the live registry the rest of the kernel looks
providers up in.

It is the consumer of
[`internal/providerresolve`](../providerresolve/README.md): given that
package's ordered list of resolved binaries, `Supervisor.Start` runs the full
per-provider bring-up sequence and `Supervisor.Shutdown` reverses it.
`Registry` is the read side — safe for concurrent use, and the type an
agent-loop consumer actually holds.

## The bring-up sequence

Per provider, in `providerresolve.Order`'s sequence:

1. **Build the late-bound callback slot.** A `callbackSlot` serving a
   provisional `internal/kernelcallback.Server`.
2. **Launch.** `internal/pluginruntime.Launch` under the category the lock
   file recorded; a `dev_overrides` provider, whose category is unknowable
   ahead of time, is probed instead (see below).
3. **`Describe`**, reconciled against the lock row. A binary contradicting the
   lock file's source, version, or recorded category is a hard startup error —
   the lock file is the source of truth for what may run
   ([`configuration.md` §11](../../docs/specifications/configuration/lock-file.md)).
4. **Verify the checksum** via `internal/registry.VerifyChecksum`. Skipped for
   a `dev_overrides` provider, which deliberately has no lock row.
5. **Fetch the capability advertisement** — `GetSchema` for tool,
   `GetCapabilities` for the other six — and with it the `ConfigSchema`.
6. **Decode the `provider{}` block** against that schema, via
   `internal/config.DecodeProviderConfig`. This is the deferred half of that
   package's documented chicken-and-egg: a `ConfigSchema` does not exist until
   the plugin is running.
7. **Install the real identity and decoded config into the slot** — *before*
   `Configure`.
8. **`Configure`, then register** into the `Registry` under
   `{described category, described name}`.

### Why step 7 comes before step 8

[`kernel-callbacks.md`](../../docs/specifications/kernel-callbacks.md) permits
a plugin to call `GetConfig` or `Log` from inside its own `Configure` handler.
But `internal/pluginruntime.Launch` needs a callback server up front, at step
2, and `internal/kernelcallback.Config` fixes both `Producer` and
`ResolvedConfig` at construction — neither of which is known that early.

`callbackSlot` resolves that: a stable server value, served on the broker from
step 2, forwarding to whichever `kernelcallback.Server` is currently installed
in it. Step 7 swaps in the finished one. The integration fixture
(`testdata/plugin`) calls `GetConfig` from inside `Configure` and compares, so
this ordering is asserted rather than assumed.

## All-or-nothing, and reverse teardown

A failure at any step of any provider tears down every provider already
launched, in reverse `LaunchIndex` order, before `Start` returns. A
half-started kernel is never handed to a session.

`Shutdown` runs under its own deadline over `context.WithoutCancel(ctx)`,
because shutdown is normally reached precisely *because* the caller's context
was canceled — inheriting that cancellation would turn every graceful drain
into an immediate kill. One plugin failing to close does not abort the rest;
the failures come back joined. It is safe after a partially failed `Start` and
safe to call twice.

## Two names, never interchangeable

`Live.LocalName` is the `agent.hcl` `required_providers` local name — the
operator's label, what a `provider{}` block and an `agent_profile` reference.
`Live.Producer.Name` is the name the plugin publishes for itself.
`blocks-reference.md#required_providers` is explicit that they need not match.
`ByLocalName` keys on the first; `ByKey` keys on the second.

## The dev-override category probe

A `dev_overrides` provider has no recorded category, so `Supervisor` launches
it once per candidate category in a fixed order and keeps the first launch
whose `Describe` answers. Up to seven subprocess launches, only ever on the
development path — see [`CLAUDE.md`](CLAUDE.md) for why this is a sequence of
single-category launches today rather than one launch keyed by all seven.
