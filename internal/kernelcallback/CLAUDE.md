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

- **`RunSession`/`CountTokens`/`Emit`/`ReadEvents`/`GetSession` are
  tracked stubs, not something to fill in opportunistically** — but for
  two different reasons, not one:
  - `RunSession` (`agent-loop.md` §7) and `CountTokens`
    (`kernel-callbacks.md` §2/§3, including the single canonical fallback
    token-count formula per `.claude/rules/determinism.md` — don't let a
    "quick" stub grow a second formula) are blocked on packages that don't
    exist yet.
  - `Emit` (`kernel-callbacks.md` §4), `ReadEvents`, and `GetSession` are
    blocked on something narrower and more specific: nothing anywhere in
    this codebase tracks which session(s) a given plugin instance is
    authorized to touch. `internal/statebackend.Store.Open` already gives
    a working data-read path (confirmed by direct check before writing
    `ReadEvents`/`GetSession`'s stubs) — the missing piece is purely the
    authorization check kernel-callbacks.md's own MUST requires ("the
    kernel MUST reject a call naming any session other than the one the
    calling plugin was actually invoked for"). Implementing the data read
    without that check would be silently insecure — any plugin could read
    any session by guessing or discovering its id — which is worse than
    an honest `codes.Unimplemented`. Don't "helpfully" wire these three up
    against `Store.Open` directly without also building that
    authorization mechanism first; that's new, separately-scoped work
    (probably wherever `Emit`'s own implementation eventually lands, since
    it needs the identical check).
  - `Emit`'s eventual implementation does not belong in this package at
    all regardless — it belongs wherever the kernel's sqlite write path
    lives, called from here, per `state-backend.md` §3's sole-writer rule.

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
