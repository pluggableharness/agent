// Package registry implements the provider-distribution subsystem
// described in specifications/configuration.md §10 (the global config file
// at $XDG_CONFIG_HOME/agent/config.hcl: dev_overrides, registry_mirror) and
// §11 (the kernel-written lock file, .agent/agent.lock.hcl). It shares
// internal/hclsecret's env(...)-only enforcement for
// registry_mirror.mirror.auth — identical to how a provider{} block's
// sensitive attributes are enforced (configuration.md §4/§6).
//
// This package does not fetch or install provider binaries. It resolves
// which URL a source address should route through (MirrorTable.Resolve)
// and verifies an already-downloaded binary's integrity against the lock
// file (VerifyChecksum) — the fetch itself is out of scope.
package registry
