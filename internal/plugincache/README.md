# plugincache

Package plugincache computes the on-disk paths for cached plugin binaries in the `$XDG_CACHE_HOME/agent/plugins/` layout.

## Purpose

This package owns path computation and filesystem presence checks for the plugin cache. It does not perform downloading, installation, or verification — those are separate, deferred concerns handled by other parts of the kernel.

## Layout

Cached plugin binaries live at:

```
$XDG_CACHE_HOME/agent/plugins/<sanitized-source>/<version>/<platform>/<binary-name>
```

Where:
- `<sanitized-source>` is the git-forge source address (e.g., `github.com/agentco/provider-anthropic`) with all forward slashes (`/`) replaced by underscores (`_`) to form a single path-safe directory segment
- `<version>` is the resolved semantic version (e.g., `1.2.3`)
- `<platform>` is the platform key in `<os>_<arch>` form (e.g., `linux_amd64`, `darwin_arm64`)
- `<binary-name>` is the trailing path segment of the source (e.g., `provider-anthropic` from `github.com/agentco/provider-anthropic`)

## Sanitization

Source addresses are deterministically sanitized by replacing `/` with `_`. This ensures the entire source address fits into a single filesystem path segment while remaining collision-resistant — different sources will always produce different sanitized forms.

Example:
- Source: `github.com/agentco/provider-anthropic`
- Sanitized: `github.com_agentco_provider-anthropic`

## Eviction (future work)

The kernel specification requires the plugin cache to be **session-log-aware** for eviction (pruning), not naive LRU/TTL. A binary is only eligible for deletion once no retained session references it. This eviction machinery is explicitly out of scope for this package and deferred to a future kernel-level implementation. For now, this package only handles path computation and presence checks.

## API

- `Platform() string` — Returns this process's platform key (`<os>_<arch>`).
- `BinaryPath(cacheDir, source, version, platform string) string` — Computes the on-disk path for a cached binary.
- `Exists(ctx context.Context, logger *slog.Logger, path string) (bool, error)` — Checks whether a binary file exists at the given path. Returns `(false, nil)` for "not found" and `(false, err)` for other stat errors to allow the caller to distinguish between "not installed" and "can't tell."
