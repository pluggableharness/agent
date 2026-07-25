// Package plugincache computes the on-disk paths for cached plugin binaries
// in the $XDG_CACHE_HOME/agent/plugins/ layout.
//
// This package handles path computation and presence checks only. It does not
// download, install, or verify plugin binaries — that is handled separately.
// See docs/specifications/architecture.md#xdg-layout for the cache directory
// semantics and docs/specifications/architecture.md#versioning--schema-drift--supersedes
// for the session-log-aware eviction requirement.
//
// Note: The eviction logic described in architecture.md (session-log-aware
// pruning instead of naive LRU/TTL) is explicitly out of scope for this
// package; it is deferred future work at the kernel level. This package
// concerns itself only with path computation and filesystem presence checks.
package plugincache
