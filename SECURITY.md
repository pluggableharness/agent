# Security Policy

PluggableHarness Agent executes shell commands, edits files, handles model-provider API keys, and launches third-party plugin subprocesses. We treat its security boundaries as product surface, not best-effort.

## Reporting a vulnerability

**Do not open a public issue.** Use GitHub's private vulnerability reporting: [Report a vulnerability](https://github.com/pluggableharness/agent/security/advisories/new) on this repository's Security tab. You'll get an acknowledgment within 5 business days, and we coordinate disclosure timing with you — we ask that you hold public details until a fix ships.

## Supported versions

Security fixes land on the latest release line. Older minor versions are not patched retroactively; upgrade to the newest release to receive fixes.

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ — upgrade |

## What counts as a vulnerability here

The interesting reports are boundary violations, in roughly this order of severity:

- **Policy-gate bypass** — any way a plugin or crafted input causes a mutation to execute without passing the kernel-owned plan/apply gate, or causes the gate to render a diff that doesn't match what executes.
- **Secret leakage into plugin subprocesses** — plugins launch with an explicit environment allowlist; anything that lets a plugin observe kernel-side secrets (API keys, tokens) outside that allowlist.
- **Provider supply-chain integrity** — checksum or lock-file verification bypass, version-resolution confusion, or cache poisoning that lets a different artifact run than the one pinned in `.agent/agent.lock.hcl`.
- **Session-log integrity** — replay executing anything (replay must be render-only), or state-backend contents being writable by an unprivileged plugin.
- **Escalation via the callback channel** — a plugin using `RunSession` or other kernel callbacks to exceed its scoped capability profile.

Ordinary bugs (crashes, wrong output, rendering glitches) without a boundary violation are regular issues — file them publicly.

## Hardening posture

Dependencies and workflows are watched by Dependabot; every push runs gosec and CodeQL, and govulncheck runs on a weekly schedule against unchanged code so newly disclosed CVEs surface without a commit. Release artifacts are built by GoReleaser in CI with published checksums — verify them before running a downloaded binary.
