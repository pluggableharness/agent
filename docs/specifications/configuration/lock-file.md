# Lock file — `.agent/agent.lock.hcl`

Kernel-written, not operator-authored — mirrors `.terraform.lock.hcl`. Lives at the project-local `.agent/` path in the XDG-adjacent layout; see [`../architecture.md#xdg-layout`](../architecture.md#xdg-layout).

```hcl
lock_file_version = 1   // MUST — format version of this file itself, independent of
                         // any individual provider's version; lets a future kernel
                         // detect and migrate an old-format lock file the same way
                         // the state backend migrates session files

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"   // MUST — when this entry was last resolved,
                                          // for audit/debugging drift over time
  checksums = {
    "linux_amd64"   = "sha256:1a2b3c..."
    "linux_arm64"   = "sha256:4d5e6f..."
    "darwin_amd64"  = "sha256:7a8b9c..."
    "darwin_arm64"  = "sha256:0d1e2f..."
  }
}
```

## Shape

- `lock_file_version` MUST be present and MUST be checked by the kernel before reading the rest of the file, mirroring the same "refuse to open something newer than understood" posture [`../state-backend.md`](../state-backend.md#schema-migration) applies to session files.
- Each `provider "<name>" { ... }` block records the resolved `source`, the exact resolved `version` (not a constraint), `resolved_at` (an RFC 3339 timestamp), and `checksums`.
- `checksums` MUST include an entry for every `(os, arch)` platform pair the kernel actually installs a binary for, not just the invoking machine's own platform — a lock file is meant to be committed and shared across a team on mixed platforms, so a checksum missing for a teammate's platform would silently break reproducibility for them specifically.

The kernel checks `lock_file_version` in an isolated pass, before the rest of the file's schema is decoded at all — this ensures a lock file written by a future, newer kernel version is refused outright rather than partially misread, mirroring [`../state-backend.md`](../state-backend.md#schema-migration)'s own migration-safety posture. A version the kernel doesn't understand MUST be a hard error.

Native HCL `{ "key" = "value" }` object-constructor syntax evaluates to an Object type, not necessarily a Map — decoding `checksums` accounts for both.

Loading the lock file logs only the file path, at `DEBUG` level — never a decoded checksum or source value.

## Checksum verification

- The kernel MUST verify a downloaded binary's checksum against the matching platform entry before executing it, **on every install** — not just the first time a version is resolved — consistent with treating the lock file as the actual source of truth for "what's allowed to run," not merely a cache hint.

Checksum verification computes the installed binary's SHA-256 digest and compares it against the recorded checksum for that platform (keyed `"<os>_<arch>"`, e.g. `"linux_amd64"`). A platform with no recorded checksum, or a digest that doesn't match, MUST both be treated as verification failures.

Checksum comparison uses plain equality rather than a timing-safe comparison, since it verifies a published binary's hash against a known-good value, not a secret token — there is no timing side-channel to defend against: an attacker who can observe comparison timing learns nothing they couldn't already get by reading the (public) lock file or the (public) release artifact. This MUST NOT be changed to a constant-time comparison; doing so would defend against a threat that doesn't apply here.

Checksum verification logs only the binary path and platform, at `DEBUG` level.

## `dev_overrides` and identity without a lock entry

A binary resolved via [`settings-and-global.md#dev_overrides`](settings-and-global.md#dev_overrides) has no `provider "<name>" { ... }` entry in this file at all — `dev_overrides` exists precisely to bypass the registry/lock-file resolution path, so there is no `source`/`version`/`checksums` for the kernel to read identity from the way it would for a normally-resolved plugin. The kernel instead obtains that plugin build's identity directly from the process itself, via that category's own `Describe` RPC — a `Describe(DescribeRequest) -> DescribeResponse { producer: common.v1.ProducerRef }` call every one of the six category protocols gains in this same protocol revision. The plugin reports its own `{name, version, source, category, protocol_version}` at connection time, rather than the kernel inferring it from a lock-file row that in this case doesn't exist. This is the canonical explanation for the general "how does the kernel know what it's actually running" question wherever a `dev_overrides` binary is in play; other specs needing to address plugin identity resolution without a lock entry should point here rather than re-deriving it.
