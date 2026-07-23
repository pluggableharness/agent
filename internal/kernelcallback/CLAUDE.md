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
  site getting it right. Don't refactor this into a shared singleton +
  interceptor "for efficiency" — a `Server` value is cheap, and the
  future plugin-runtime broker wiring is expected to construct one per
  launched plugin, not reuse one across plugins.
- **`RunSession`/`CountTokens`/`Emit` are tracked stubs, not something to
  fill in opportunistically.** Each returns `codes.Unimplemented` with its
  own message. Do not implement real logic for any of the three here
  without a separate task scoped to that RPC's actual semantics
  (`agent-loop.md` §7 for `RunSession`; `kernel-callbacks.md` §2/§3 for
  `CountTokens`, including the single canonical fallback token-count
  formula per `.claude/rules/determinism.md` — don't let a "quick"
  `CountTokens` stub grow a second formula; `kernel-callbacks.md` §4 for
  `Emit`, including that the kernel is the state backend's sole writer per
  `state-backend.md` §3, so `Emit`'s eventual implementation does not
  belong in this package at all — it belongs wherever the kernel's sqlite
  write path lives, called from here).
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
  and keeps `Server` forward-compatible if the proto ever adds a fifth RPC.
  The embed is a compile-time forward-compatibility guard only; every
  method the interface currently declares is still explicitly implemented
  on `Server` (three as stubs, one as a real delegation) rather than left
  to fall through to the embedded unimplemented methods, so
  `go vet`/interface satisfaction doesn't silently hide a missing method
  later.
