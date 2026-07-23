# internal/pluginruntime — agent notes

- **Fixed callback broker ID, not a wire-negotiated one — and this is
  deliberate, not a shortcut.** `go-plugin`'s bidirectional broker
  normally passes a dynamically-assigned `uint32` stream ID through an
  application RPC field so both sides `AcceptAndServe`/`Dial` the same ID.
  No message in any of the six category protos carries such a field
  (confirmed by direct proto read during design), and adding one was
  explicitly out of scope for this task. `pkg/common.CallbackBrokerID = 1`
  is an out-of-band constant both sides compile against instead — the same
  trick the magic cookie already uses. This is safe because the kernel is
  the *only* party that ever calls `broker.AcceptAndServe` (never
  `broker.NextId()`), so there's no collision risk. Don't "fix" this by
  adding a broker-ID field to a proto — that was considered and rejected.

- **This package never constructs the `kernelcallback.Server` it serves.**
  `Config.Callback` is a `kernelv1.KernelCallbackServiceServer` the
  *caller* builds — one `internal/kernelcallback.Server` instance per
  launched plugin, with that plugin's already-resolved `ProducerRef` baked
  in (`internal/kernelcallback/CLAUDE.md`'s "one Server per plugin
  instance" design). This package's job is purely to serve whatever it's
  handed on the fixed broker ID via `categoryPlugin.newCallbackServer`
  (`adapter.go`). Don't add a constructor for `kernelcallback.Server` here
  — that would duplicate a decision that already belongs to that package.

- **Env allowlist, never `os.Environ()` — `buildEnv` in `launch.go`.** A
  launched subprocess gets exactly `PATH`/`HOME`/`TMPDIR` (only if set in
  the kernel's own environment), the `OTEL_RESOURCE_ATTRIBUTES` producer
  stamp (`telemetry.ResourceEnv`), and `Config.ExtraEnv` — nothing else.
  `configuration.md`'s secret model is explicit-injection-only; ambient
  inheritance would leak every env var the kernel process holds, including
  secrets meant for *other* plugins, into every subprocess. Don't add a
  "just pass through the rest of the environment" fallback, even for
  debugging convenience.

- **The step-1 pre-flight version check (`preflightVersionCheck`) is a
  no-op today, on purpose.** Nothing populates `ProducerRef.ProtocolVersion`
  anywhere yet — no registry/lockfile field carries one. The check already
  handles a real value correctly (reject before spawning anything) so it
  doesn't need touching once something does populate it; it's a real gate
  today only in the trivial "0 always passes" sense. Step 6's
  post-handshake `client.NegotiatedVersion()` check is the sole
  *authoritative* gate regardless of whether step 1 ever does anything —
  don't remove step 6 on the theory that step 1 makes it redundant.

- **`defaultDrainTimeout = 5 * time.Second` (`shutdown.go`) is a hardcoded
  package constant, not a `Config` field.** This was a deliberate
  operator decision, not an oversight — don't add a
  `Config.DrainTimeout` field to make it configurable; if that's ever
  needed, it's a scoped follow-up, not a "quick fix" folded into an
  unrelated change.

- **The hclog shim (`hcloglogger.go`) carries `go-plugin`'s own
  subprocess-management diagnostics — handshake negotiation, broker
  bring-up, process-exit bookkeeping — never plugin *application* logs.**
  A plugin's own log output crosses `KernelCallbackService.Log` and lands
  in `internal/kernelcallback.Server` / `internal/log.Server` instead. Do
  not repurpose `hclogAdapter` to carry application logs "since it's
  already a slog bridge" — that would double-log plugin output through two
  unrelated paths with different attribution.

- **The chicken-and-egg between `GRPCDialOptions` and `*plugin.Client`
  (`crash.go`'s `clientHolder`, used from `buildClient` in `launch.go`).**
  `plugin.NewClient(&plugin.ClientConfig{...})` needs `GRPCDialOptions` —
  which need to close over `client.Exited()` for crash classification —
  before the `*plugin.Client` they'd call it on exists. `clientHolder`
  breaks this: the interceptors close over `holder.exited` (a method
  value bound to the holder, not to any client), `plugin.NewClient` is
  called, and *only then* is the resulting client stored into the holder
  — all before the client is ever dialed, so no RPC can observe an unset
  holder. Don't try to simplify this into passing `client` directly into
  the dial options closure; the client doesn't exist yet at that point.

- **`buildClient` is split out of `Launch` specifically so most of the
  launch sequence is unit-testable without spawning a real subprocess.**
  `plugin.NewClient` only builds a struct — confirmed by reading
  `go-plugin`'s own source (`NewClient` in `client.go`) — nothing is
  spawned or dialed until `client.Client()` is called. `buildClient`
  covers launch steps 2-4 (plugin map, `exec.Cmd` construction, client
  construction) and is exercised directly by `launch_test.go`'s unit
  tests; `Launch` itself only adds steps 5-8 (the actual
  spawn+handshake+gate+dispense), which — per `go-testing.md` — must not
  run in a unit test, so those steps are exercised exclusively by
  `launch_integration_test.go`. If you're tempted to inline `buildClient`
  back into `Launch` "for readability," you'll also be deleting most of
  this package's unit-tier coverage of the launch sequence.

- **`categoryPlugin.GRPCClient` (`adapter.go`) has no unit test — this is
  a confirmed, not assumed, limitation.** It takes a `*plugin.GRPCBroker`,
  whose only constructor (`newGRPCBroker`) is unexported and requires an
  unexported `streamer` type this package cannot supply from outside
  `github.com/hashicorp/go-plugin`. The "broker-serve-once" logic it
  relies on (`categoryPlugin.brokerOnce`, a `sync.Once`) is still unit
  tested directly (`adapter_test.go`'s `TestCategoryPlugin_brokerOnce`);
  only the full `GRPCClient` method — and the "`AcceptAndServe` was
  actually called" assertion — lives in the integration tier
  (`launch_integration_test.go`), which exercises it against a real
  broker via a real subprocess round-trip. Don't spend time trying to
  fake `*plugin.GRPCBroker` in-process; it was investigated and isn't
  possible without forking `go-plugin` itself.

- **Unstarted `*plugin.Client` values are safe to construct and `Kill()`
  in tests — this is what makes `shutdown_test.go` and several
  `crash_test.go`/`launch_test.go` cases possible without a real
  subprocess.** `Kill()` checks `runner == nil` (never started) and
  returns immediately as a no-op; `Exited()` and `NegotiatedVersion()`
  return their zero values. Constructing a real `*plugin.Client` via
  `plugin.NewClient(&plugin.ClientConfig{Cmd: exec.Command("true"), ...})`
  and never calling `.Client()`/`.Start()` on it is the sanctioned pattern
  here for testing against the real `hashicorp/go-plugin` types instead of
  a hand-rolled fake — reach for it before inventing a `Client`-shaped
  interface to fake.

- **`Close`'s drain-then-escalate logic (`closeWithKill` in
  `shutdown.go`) is deliberately factored apart from `*plugin.Client`
  itself**, taking `killFn func()` and `cancelLaunch context.CancelFunc`
  as plain function values rather than a concrete `*Plugin`. This is what
  lets the timing behavior (races `killFn` against the drain deadline,
  escalates to `cancelLaunch` only on timeout) be unit-tested
  deterministically with fake, controllable `killFn`s — a real `Kill()`
  either returns near-instantly (never-started client) or requires an
  actual hung subprocess to exercise the escalation path, neither of
  which unit tests can rely on for the escalation branch specifically.

- **The integration fixture (`testdata/plugin/main.go`) is the one place
  in this whole package that writes the plugin *side* of the adapter.**
  It exists purely to give `launch_integration_test.go` something real to
  dial — build-tagged `integration` so it's excluded from the default
  build, and living under `testdata/` so `go build ./...` skips it
  regardless. Don't mistake it for the start of a real plugin-side SDK;
  that's explicitly out of scope for this package (see README.md).
