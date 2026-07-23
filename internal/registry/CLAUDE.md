# internal/registry — agent notes

- **`LoadLockFile` checks `lock_file_version` via a separate
  `PartialContent` pass before decoding the rest of the file** —
  `configuration.md` §11 requires refusing a lock file newer than
  understood *before* attempting to read anything else in it, mirroring
  `state-backend.md` §9.1. Don't collapse this into a single `Content`
  call; the whole point is failing before the rest of the schema is even
  applied.
- **`checksums = { ... }` decodes via `cty.Value.AsValueMap()`**, which
  works for both HCL's Map and Object constructor types — native HCL
  `{ "key" = "value" }` syntax evaluates to an Object type, not
  necessarily a Map, so don't switch this to a Map-only accessor.
- **`VerifyChecksum` uses plain string equality, not `hmac.Equal` or any
  timing-safe comparison** — deliberate, documented in the function's own
  comment: this verifies a published binary's hash against a known-good
  value, not a secret token, so there's no timing side-channel to defend
  against. Don't "harden" this with a constant-time comparison; it's
  solving a problem that doesn't apply here.
- **`decodeMirror`'s `auth` handling is optional** (`Required: false`) —
  a mirror with no `auth` field is legal; only validate/evaluate it when
  present.
- Shares `internal/hclsecret` with `internal/config` for the `env(...)`-only
  enforcement — don't reimplement that check locally here.
- **`LoadGlobalConfig`, `LoadLockFile`, and `VerifyChecksum` are
  instrumented per `internal/CLAUDE.md`'s file-I/O mandate**, mirroring
  `internal/config`'s `LoadFile`. Signatures are
  `LoadGlobalConfig(ctx context.Context, prov *telemetry.Provider, path string) (*GlobalConfig, error)`,
  `LoadLockFile(ctx context.Context, prov *telemetry.Provider, path string) (*LockFile, error)`,
  and `VerifyChecksum(ctx context.Context, prov *telemetry.Provider, binaryPath, platform string, locked LockedProvider) error`
  — `ctx` first, then `prov`, per `go-style.md`. Each opens its matching
  `prov.Start*` span from `internal/telemetry/span.go`
  (`StartGlobalConfigLoad`/`StartLockFileLoad`/`StartChecksumVerify`), ends
  it via `telemetry.EndSpan` in a `defer` closing over a named `err`
  return — every return path assigns to that named `err`, or the deferred
  `EndSpan` silently records a false "success" on a failed load, exactly
  the failure mode `internal/config/CLAUDE.md` calls out for `LoadFile`.
  Each also logs one `slog.DebugContext` entry on entry carrying only the
  path(s)/platform — never a decoded value, and `globalconfig.go`'s
  `auth` field specifically is captured only for later `env()` evaluation
  and is never logged. No `ERROR` log is added inside any of the three —
  they return their errors, and `go-style.md`'s "a function returns an
  error or logs it, never both" applies here like any other package.
- **`RegistryMirror.Resolve` and the unexported HCL decode helpers
  (`decodeDevOverrides`, `decodeRegistryMirror`, `decodeMirror`,
  `attrString`, `decodeLockedProvider`, `lockFileVersion`) are
  deliberately left uninstrumented — don't "complete the pass" by adding
  telemetry to them.** All are in-memory only (no file I/O, no
  process/network boundary), so none trips `internal/CLAUDE.md`'s
  "performs I/O" trigger — identical reasoning to why
  `internal/config`'s `decode`/`DecodeProviderConfig` stay uninstrumented.
- Test helpers live in `helpers_test.go`: `testProvider`/
  `testProviderWithBackend` build a `*telemetry.Provider` wired to
  `internal/telemetry/drivers/fake` (mirrors
  `internal/config/helpers_test.go`), `flushedSpans` force-flushes and
  reads back recorded spans, and `captureLogs` installs a temporary
  DEBUG-level `slog` default to assert an entry log fired. `captureLogs`
  mutates the process-wide `slog` default, so a test using it must not
  call `t.Parallel()` on itself (subtests of *other* top-level test
  functions in this package are safe since Go runs non-parallel top-level
  tests one at a time) — see `internal/telemetry/CLAUDE.md`'s own note
  about ambient global state racing against `t.Parallel()`.
