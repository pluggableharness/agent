# internal/kernelcallback — agent notes

- **One `Server` per plugin instance, deliberately — not a shared server
  plus an interceptor.** The alternative design (a single
  `KernelCallbackServiceServer` shared across every launched plugin, with a
  gRPC interceptor or context value supplying producer identity per call)
  was rejected: identity is a property of *which broker connection a call
  arrived on*, established at handshake
  (`kernel-callbacks.md` §4/§5 — "server-derived, never client-supplied").
  Binding identity into the `Server` value at construction makes that
  property structurally true instead of relying on every interceptor call
  site getting it right. This now extends to every other per-plugin
  dependency `Config` carries (`Telemetry`, `TelemetryRelay`, `Bus`,
  `ResolvedConfig`) — none of them are shared across plugin instances
  either, for the identical reason. Don't refactor this into a shared
  singleton + interceptor "for efficiency" — a `Server` value is cheap,
  and the future plugin-runtime broker wiring is expected to construct one
  per launched plugin, not reuse one across plugins.

- **`RunSession` is the one remaining tracked stub.** `agent-loop.md` §7
  defines the session-tree semantics it will eventually carry out; this
  build is root-sessions-only, so it stays `codes.Unimplemented` rather
  than a partial implementation. `CountTokens`/`Emit`/`ReadEvents`/
  `GetSession` are now implemented (`tokens.go`/`emit.go`/`events.go`/
  `sessions.go`) — don't reintroduce a stub for any of them "to be safe."

- **The session-authorization gate (`sessions.go`'s `authorizedSession`)
  returns the identical `codes.PermissionDenied` error — the shared
  `errNotAuthorized` value — whether a plugin was never granted the named
  session, or was granted it but the session is no longer live (`Table.Get`
  misses). This indistinguishability is a deliberate security property,
  not a bug to "fix" into two error codes later**: `codes.NotFound` (or
  any code/message that let a caller tell the two failure modes apart)
  would let a caller probe for the existence of sessions it has no
  business knowing about — exactly what kernel-callbacks.md's MUST
  ("the kernel MUST reject a call naming any session other than the one
  the calling plugin was actually invoked for") exists to prevent. Every
  session-scoped RPC (`Emit`, `ReadEvents`, `GetSession`) goes through this
  one helper rather than each reimplementing the check — don't add a
  second authorization path.

- **`Emit`'s implementation does not itself write to sqlite** — it
  validates and delegates to the authorized session's
  `*sessionstate.Live.Emit`, per `state-backend.md` §3's sole-writer rule.
  It rejects `EVENT_KIND_MESSAGE`/`EVENT_KIND_PLAN` outright
  (`kernelOwnedEventKinds` in `emit.go`) because only
  `sessionstate.Live.EmitMessage`/`EmitPlan` — called from a future
  kernel-internal path, never this RPC — can populate `cost_ledger`/
  `plan_items` in the same transaction as their event, which a generic
  plugin-facing `Emit(kind, payload)` call structurally cannot guarantee.
  Don't "simplify" by routing those two kinds through the plain `Emit`
  path; see `internal/sessionstate/CLAUDE.md`'s identical rule.

- **`sessionstate.Live` gained three additive, unlocked read
  pass-throughs (`Meta`, `TotalCostUSD`, `Events`) in
  `internal/sessionstate/query.go`, specifically so `ReadEvents`/
  `GetSession` never need to reach around `Live`'s sole-writer abstraction
  to import `internal/statebackend` directly.** This package still MUST
  NOT import `internal/statebackend` for anything beyond what
  `sessionstate`/`sessionscope` already re-expose.

- **`GetSession`'s `RemainingDepth` is a fixed placeholder
  (`rootSessionRemainingDepth` in `sessions.go`, `math.MaxInt32`), not a
  real depth-budget read — this build is root-sessions-only.** Nothing in
  this codebase yet wires a live, per-session depth tracker the way
  `bounds.Tracker` already does for cost (`internal/agentprofile`'s
  `RootRemainingDepth`/`ChildRemainingDepth` compute the *number* per
  `configuration.md` §8.4, but nothing tracks it live per session). Report
  the honest "effectively unbounded" sentinel rather than fabricate a
  ceiling this build can't enforce; a future phase adding real depth-budget
  tracking replaces the constant with a live read and should delete this
  note along with it.

- **`internal/log.Server` is intentionally untouched by this package.**
  `Server.Log` here does exactly two things: inject this instance's fixed
  producer via `producer.WithProducer`, then call straight through to the
  wrapped `log.Server.Log`. Don't duplicate any of `internal/log`'s entry
  validation, level translation, or attribute-building logic here — it
  already lives in exactly one place.

- **`Server` embeds
  `kernelv1.UnimplementedKernelCallbackServiceServer` by value** (per the
  generated type's own doc comment, to avoid a nil-pointer dereference) —
  this is what satisfies `mustEmbedUnimplementedKernelCallbackServiceServer()`
  and keeps `Server` forward-compatible if the proto ever adds another RPC.
  The embed is a compile-time forward-compatibility guard only; every
  method the interface currently declares is still explicitly implemented
  on `Server` (five as stubs, seven with real logic) rather than left to
  fall through to the embedded unimplemented methods, so `go vet`/interface
  satisfaction doesn't silently hide a missing method later.

- **`Publish`/`Subscribe`'s topic construction and `RecordMetrics`'
  instrument-name construction share one helper, `producerScopedName`
  (`telemetry.go`), and one lowercase category-text table, `categoryTextTable`
  (`category.go`).** `category.go`'s table is a deliberate, independent
  copy of `internal/statebackend`'s own `producerCategoryText` — not an
  import of it. The two happen to agree on every value today, but they're
  conceptually owned by different specs (state-backend.md's storage
  encoding vs. event-bus.md's wire-facing topic grammar); don't "simplify"
  by importing `internal/statebackend` into this package just to
  deduplicate seven map entries.

- **`Subscribe`'s bounded bridge (`eventbus.go`) is a second, additional
  bound layered on top of `internal/eventbus`'s own unbounded, never-drop
  contract — it does not change that contract.** The bridge's `events`
  channel (capacity `Server.busSubscribeQueueBound`) sits between
  `internal/eventbus`'s own delivery goroutine (which still never blocks
  or drops) and the gRPC stream's `Send` calls. When `events` is full, the
  handler signals `overflow` (buffered 1, non-blocking) instead of
  blocking; the main select loop only observes that signal once its
  current, possibly slow, `stream.Send` call returns — so don't expect
  the stream to close *immediately* on overflow if a `Send` call happens
  to be in flight when the bound is exceeded. `TestServer_Subscribe_backpressureCloses`
  exercises this exact sequencing (close the test's blocking `release`
  channel *before* waiting on `done`, not after — the same
  close-before-wait ordering `internal/eventbus/CLAUDE.md` already
  documents biting that package's own first test draft).

- **`GetConfig`'s handler never logs `req` or its own return value.**
  `kernel-callbacks.md`'s GetConfig section restates the MUST NOT-echo
  rule other RPCs already carry, specifically because `GetConfig` is a
  second channel a `sensitive`-marked config value can cross. Don't add an
  entry-level log line that includes the resolved config Struct, even at
  `TRACE` — see `config.go`'s own comment.
