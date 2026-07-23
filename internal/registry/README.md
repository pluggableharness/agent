# internal/registry

The provider-distribution subsystem from `specifications/configuration.md`
§10 (global config, `$XDG_CONFIG_HOME/agent/config.hcl`) and §11 (the
kernel-written lock file, `.agent/agent.lock.hcl`).

## What this package does

- `globalconfig.go` — `LoadGlobalConfig(ctx, prov, path)`: `dev_overrides`
  (local provider name → local binary path) and `registry_mirror` (default
  URL plus per-prefix `mirror{}` redirects, `auth` enforced via
  `internal/hclsecret` identically to a provider's sensitive attributes).
- `mirror.go` — `RegistryMirror.Resolve`: longest-prefix-wins source
  redirection.
- `lockfile.go` — `LoadLockFile(ctx, prov, path)`: checks
  `lock_file_version` before decoding anything else, then per-provider
  `source`/`version`/`resolved_at`/`checksums`.
- `checksum.go` — `VerifyChecksum(ctx, prov, binaryPath, platform, locked)`:
  sha256 integrity check against a lock file's recorded per-platform
  checksum.

## Instrumentation

`LoadGlobalConfig`, `LoadLockFile`, and `VerifyChecksum` all perform file
I/O, so per `internal/CLAUDE.md` each takes a leading `ctx context.Context,
prov *telemetry.Provider` pair, opens the matching `prov.Start*` span
(`StartGlobalConfigLoad`/`StartLockFileLoad`/`StartChecksumVerify` from
`internal/telemetry/span.go`), ends it via `telemetry.EndSpan`, and logs one
`slog.DebugContext` entry on entry carrying only the path(s)/platform (never
a decoded value). `RegistryMirror.Resolve` and the unexported HCL decode
helpers (`decodeDevOverrides`, `decodeRegistryMirror`, `decodeMirror`,
`attrString`, `decodeLockedProvider`, `lockFileVersion`) are deliberately
left uninstrumented — pure in-memory logic with no I/O boundary to log or
trace.

## How it fits in

This package does **not** fetch or install provider binaries — only
resolves which URL a source should route through and verifies an
already-downloaded binary's integrity. The actual download/install flow,
and whatever calls `VerifyChecksum` on every install (not just first
resolution, per §11), doesn't exist yet.
