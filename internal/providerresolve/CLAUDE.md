# internal/providerresolve — agent notes

- **`Resolve` accumulates; it never returns on the first problem.** This
  is the package's reason for existing, not a nicety. Adding an early
  `return` on a failed provider — for a "fast path", for a clearer error,
  for anything — turns a one-pass `terraform init`-style report into a
  fix-one-rerun-repeat loop across a whole `required_providers` block.
  `TestResolve_accumulatesEveryProblem` is the guard; it asserts four
  distinct failures in a single call, in `Order`'s sequence.

- **`Order` is used for more than ordering this package's own output.**
  The sequence it produces later becomes launch order in
  `internal/pluginhost`, which is hook-dispatch order, whose reverse is
  shutdown order. Changing the tiebreak rules here silently reorders hook
  dispatch. If a new tiebreak is genuinely needed, it belongs in `Order`
  (one place), never re-derived by a consumer.

- **Entries with no `provider{}` block are legal, not an error.** A
  provider declared in `required_providers` and never configured has no
  textual position. It sorts after every positioned entry, then by name,
  and resolves normally. Don't "tighten" this into a validation failure —
  a provider taking no config is an ordinary case
  (`blocks-reference.md`'s `provider{}` body is entirely optional).

- **This package instruments with `slog` only — no `internal/telemetry`
  span, deliberately.** Its only I/O is `os.Stat`, delegated to
  `internal/plugincache`, whose own `CLAUDE.md` records the same call:
  "the overhead of an OTel span would exceed the actual work being done."
  Opening a span here would contradict that decision one layer up for the
  identical syscall. Don't "complete the instrumentation pass" by adding
  one. The `Provider.Start*` spans that *do* cover this territory
  (`StartChecksumVerify`, `StartPluginLaunch`) belong to the components
  that actually verify and launch — `internal/registry` and
  `internal/pluginhost`.

- **`Input.Logger` is a real field, not an oversight in the sketch this
  package was built from.** `plugincache.Exists` takes a
  `*slog.Logger` positionally and dereferences it, so a nil logger
  panics; `Resolve` defaults it to `slog.Default()` rather than passing
  nil through.

- **A `dev_overrides` binary is still existence- and
  executable-checked.** The bypass in
  `settings-and-global.md#dev_overrides` is of the *registry/
  version-constraint machinery* — the lock row and the checksum — not of
  "is this file actually runnable". A typo'd override path is reported as
  `MissingNotCached` alongside every other problem, rather than handed to
  `internal/pluginhost` to fail as an `exec` error one provider at a time.
  Don't remove those two checks on a reading of "bypass" that covers them.

- **`executable` keys off `runtime.GOOS`, not `Input.Platform`.** The
  mode bits belong to the local filesystem holding the binary; the
  platform key describes which build the cache path names, and the two
  can legitimately differ (inspecting a cache populated for another
  platform). Windows is skipped entirely — it has no POSIX mode bits and
  decides executability from the file extension, so checking there would
  report every binary as `MissingNotExecutable`.

- **`parseCategory` returns `CATEGORY_UNSPECIFIED` for an unrecognized
  lock-file category string, and that is not an error.**
  `registry.LockedProvider.Category` is documented as a *cache* of an
  already-discovered category. A garbled or stale value costs one live
  `Describe` probe in `internal/pluginhost`, which is exactly what
  happens for the (common) empty case anyway. Don't promote it to a
  validation failure — that would make a lock file written by a newer
  build with an eighth category name unloadable by an older one.

- **`categoryText` is an independent copy of the same seven strings
  `pkg/common.PluginKey` produces, on purpose.** `PluginKey` maps
  enum → go-plugin map key; this maps lock-file text → enum. They agree
  today, but they answer to different specs (`plugin-runtime.md`'s
  handshake key grammar vs. `lock-file.md`'s stored encoding) — the same
  reasoning `internal/kernelcallback/CLAUDE.md` already records for its
  own `categoryTextTable`. Don't collapse them into one shared table.
