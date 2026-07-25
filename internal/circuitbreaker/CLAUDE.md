# internal/circuitbreaker — agent notes

- **Denials and crashes share ONE per-provider signal — this is a
  deliberate design decision, not an oversight.** `RecordDenial` and
  `RecordCrash` both call the same internal `record(provider, bad=true)`
  path and increment the exact same consecutive-count and sliding-window
  state for that provider. There is no separate "denial counter" vs "crash
  counter." Reasoning: `error-recovery.md#tool-provider-plugin-crashes`
  says repeated crashes "SHOULD trip the same circuit-breaker mechanism
  described for denials... since an infinite crash-retry loop is the
  failure-mode analog of a denial storm" — read as one underlying signal
  ("this provider is repeatedly failing"), not two independent ones each
  needing its own threshold. If a future implementer decides the two call
  sites actually need independent counters (e.g. because a provider that's
  flaky-but-never-denied shouldn't share budget with one that's
  denied-but-never-crashes), that's a real, defensible alternative the spec
  doesn't foreclose — but it requires either two `Breaker` instances per
  provider (one per event kind) at the call site, or a package API change
  here (e.g. `RecordDenial`/`RecordCrash` writing to distinct sub-counters
  under one `Config`). Don't silently split the signal without updating
  this note and doc.go.
- **The sliding window is a ring buffer, not a growing-then-trimmed
  slice.** `providerState.window` is allocated once at `WindowSize` and
  reused via `windowPos`/`windowLen`; `windowBad` is maintained
  incrementally in `slideWindow` (increment/decrement on the one entry
  that changes) rather than recomputed by scanning the buffer on every
  call. Don't "simplify" this back to `append` + reslice — that either
  grows the backing array unboundedly over a long session or requires a
  full rescan per event to recompute the bad count.
- **A provider's state is created lazily on first event**, in `record`,
  not in `New`. There is no way to pre-register a provider name, and there
  doesn't need to be — an absent provider behaves identically to a
  never-tripped one until its first `RecordDenial`/`RecordCrash`/`RecordSuccess`.
- **`Reset` deletes the map entry rather than zeroing it in place.** The
  next event for that provider re-allocates a fresh `providerState`
  (including a fresh window buffer). This is simpler than resetting every
  field by hand and costs nothing extra since providers aren't hot enough
  for allocation to matter here (one event per tool call/turn, not per
  token).
- **Both `record` (unexported) and the public `RecordDenial`/`RecordCrash`
  are simple wrappers over the same bad-event path.** If a future change
  needs kind-specific behavior (e.g. the shared-signal decision above gets
  revisited), start there — don't add a parallel code path.
