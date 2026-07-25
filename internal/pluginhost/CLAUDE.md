# internal/pluginhost — agent notes

- **`callbackSlot` exists because of a real ordering constraint, not as
  indirection for its own sake — don't "simplify" it away.**
  `internal/pluginruntime.Launch` requires a
  `kernelv1.KernelCallbackServiceServer` up front (go-plugin serves it on
  the broker during the dispense `Launch` performs), while
  `internal/kernelcallback.Config` fixes `Producer` and `ResolvedConfig`
  at construction — deliberately, per that package's own `CLAUDE.md`.
  Neither value exists at launch time: identity arrives with `Describe`,
  and the resolved config can only be decoded once `Describe`'s schema
  fetch has happened. The slot forwards to a swappable server so
  `Supervisor` can install the finished one before issuing `Configure`,
  which `kernel-callbacks.md` explicitly lets a plugin call `GetConfig`
  and `Log` from inside. Constructing the callback server after the
  launch is not an option; passing a mutable `kernelcallback.Server` is
  not either.

- **`callbackSlot` forwards all twelve RPCs explicitly, even though it
  embeds `UnimplementedKernelCallbackServiceServer`.** The embed is a
  forward-compatibility guard only. A method left to fall through would
  silently answer `Unimplemented` for a callback
  `internal/kernelcallback` genuinely implements, and nothing — not the
  compiler, not `go vet` — would say so.
  `TestCallbackSlot_forwardsEveryRPC` is the guard; add a case to it
  alongside any new RPC.

- **The dev-override category probe is a *sequence* of single-category
  launches, and that is a deliberate accommodation, not the end state.**
  `internal/pluginruntime`'s `pluginMap` is variadic and its
  `launchScope` was built specifically so one subprocess can be keyed by
  all seven categories at once — its own `CLAUDE.md` describes exactly
  that probe. But no *exported* entry point reaches it: `Launch` takes
  one `Config` with one `Producer.Category` and dispenses one client
  (confirmed by direct read, not assumed). So this package probes by
  launching up to seven times, closing each non-answering attempt. If
  `internal/pluginruntime` ever exports a multi-category launch, replace
  `Supervisor.probe`'s loop with it — the cost is real (seven fork/execs
  worst case), it is just bounded and confined to the development path.

- **`Supervisor.launch` is a field, not a method call, purely for
  testability — and that seam is load-bearing for this package's
  coverage.** Everything after the fork/exec (Describe, reconcile,
  checksum verify, schema fetch, config decode, the slot install,
  Configure, register, and every failure path's teardown) runs for real
  in the unit tier against an in-process gRPC client, because
  `start_test.go` substitutes a fake `launchFunc`. Inline
  `spawnSubprocess` back into `startOne` and most of the sequence becomes
  integration-only. Same reasoning `internal/pluginruntime` records for
  factoring `buildClient` out of `Launch` and `closeWithKill` out of
  `Close`. `Live.closeFn` is the same seam for `Shutdown`'s ordering and
  error aggregation.

- **`rpc.go` is the *only* file that knows a category's RPC names.**
  Three switches, one per operation, all in one file so an eighth
  category means editing one place. Two asymmetries there are real and
  will look like bugs if you skim: tool's advertisement RPC is
  `GetSchema`, not `GetCapabilities`; and the `ConfigSchema` is nested
  inside a per-category `Capabilities` message for
  model/context/memory/frontend/widget but sits flat on the response for
  tool and slashcommand. `TestRPCDispatch_everyCategory` pins all seven
  by giving each fake server a schema attribute named after its own
  category, so a mis-wired case fails with the wrong name rather than
  passing on a nil.

- **`Config.Scopes`/`Config.Sessions`/`Config.Tokens` are wired straight
  through to `internal/kernelcallback.Config` in `newCallbackServer`, one
  shared instance of each across every launched plugin's server — this
  was a gap discovered and fixed post-merge: this package was originally
  built before `internal/kernelcallback`'s session-authorization
  completion landed, so its `newCallbackServer` didn't pass them and
  `NewSupervisor` didn't require them, which meant a real launched plugin
  calling `Emit`/`ReadEvents`/`GetSession`/`CountTokens` would nil-pointer
  panic inside `internal/kernelcallback`. All three are now MUST-be-set
  in `Config.validate()`, matching `internal/kernelcallback`'s own
  MUST-be-set convention for the same fields. Don't make any of them
  optional again — that reintroduces the exact panic this fix closed.

- **`reconcile` deliberately does not compare `Producer.Name` to
  anything.** The lock file records source, version, and (optionally)
  category — never a published name — and
  `blocks-reference.md#required_providers` explicitly permits the
  `required_providers` local name to differ from the plugin's own. A
  name check here would reject the documented normal case.

- **`Registry.Add` rejects a duplicate rather than overwriting, and
  `Start` treats that as fatal.** v1 has exactly one instance per
  `required_providers` entry and no Terraform-style `alias`
  (`blocks-reference.md#required_providers`), so two plugins claiming one
  `{category, name}` is a configuration error. Overwriting would leave
  the first plugin's subprocess running but permanently unreachable.

- **`Live.Capabilities` is retained even though this package only reads
  `ConfigSchema` out of it.** It is the whole per-category response
  (`*toolv1.GetSchemaResponse`, `*modelv1.GetCapabilitiesResponse`, …),
  kept because it is the only place a future
  `providercatalog/drivers/plugin` can read per-model specs, per-tool
  schemas, and subscribed hook points from without a second round trip.
  Don't drop it as unused.

- **Only the small `ModelClientByLocalName` adapter lives in
  `adapters.go` — a full `providercatalog.Catalog` adapter deliberately
  does not.** `Catalog` needs resolved `ModelHandle`/`ToolHandle`/
  `ContextHandle` values with per-model specs, per-operation schemas,
  `SupportsPreview`, and agent.hcl-override token budgets — that is a
  catalog *builder*, which is `providercatalog/drivers/plugin`'s job (its
  own `CLAUDE.md` anticipates exactly that driver). Building it here
  would put catalog policy in the lifecycle package. `Live.Capabilities`
  is what makes that driver possible later.

- **This package never imports `internal/providercatalog` or
  `internal/tokencount`, and must stay that way.** Both declare their
  own consumer-side interfaces and explicitly document that a registry
  should satisfy them structurally; `adapters.go` is that satisfaction
  from this side.

- **The integration fixture's identity comes from `-ldflags`, not the
  environment — this is not a style choice.** `internal/pluginruntime`
  launches every subprocess under a minimal `PATH`/`HOME`/`TMPDIR`
  allowlist and never inherits the kernel's environment (its
  `CLAUDE.md`'s env-allowlist decision), so a `t.Setenv` in a test would
  never reach the plugin. `TestMain` builds two binaries from the one
  source file with `-X main.fixtureName=...` to get two distinct
  published identities. If you need a third variant, add another build —
  don't reach for an env var, and don't add an `ExtraEnv` passthrough to
  `Supervisor` to make one work.
