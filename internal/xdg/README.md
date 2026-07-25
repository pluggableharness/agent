# xdg

Pure-domain package that resolves the kernel's XDG Base Directory layout into concrete paths. The kernel uses these paths to locate project config, global config, cache, persistent data, and session state.

## Paths

The package resolves six XDG paths and derives eight concrete filesystem locations:

| Path | Environment | Purpose |
|---|---|---|
| `./agent.hcl` | — | Root config (project-local) |
| `./.agent/agent.lock.hcl` | — | Lock file (project-local, resolved versions + checksums) |
| `$XDG_CONFIG_HOME/agent/` | `XDG_CONFIG_HOME` | Global CLI config, credentials, dev overrides |
| `$XDG_CONFIG_HOME/agent/config.hcl` | — | Global config file |
| `$XDG_CACHE_HOME/agent/` | `XDG_CACHE_HOME` | Downloaded plugin binaries (keyed by name/version/platform/checksum) |
| `$XDG_CACHE_HOME/agent/plugins/` | — | Plugin cache subdirectory (layout managed by `internal/plugincache`) |
| `$XDG_DATA_HOME/agent/` | `XDG_DATA_HOME` | Persistent plugin data |
| `$XDG_STATE_HOME/agent/` | `XDG_STATE_HOME` | Session state and transcripts |
| `$XDG_STATE_HOME/agent/sessions/` | — | Session files subdirectory (one sqlite file per session) |

XDG environment variables follow the [XDG Base Directory specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html) fallback defaults when unset:

- `XDG_CONFIG_HOME` defaults to `$HOME/.config`
- `XDG_CACHE_HOME` defaults to `$HOME/.cache`
- `XDG_DATA_HOME` defaults to `$HOME/.local/share`
- `XDG_STATE_HOME` defaults to `$HOME/.local/state`

## API

```go
// Paths holds all filesystem locations the kernel needs.
type Paths struct {
    ProjectConfig  string
    LockFile       string
    ConfigDir      string
    GlobalConfig   string
    CacheDir       string
    PluginCacheDir string
    DataDir        string
    StateDir       string
    SessionsDir    string
}

// Resolve computes Paths for a kernel with the given projectDir
// (typically the working directory; pass an absolute path).
func Resolve(projectDir string) (Paths, error)
```

## Responsibility boundaries

- **This package**: Path computation only. No file I/O beyond `os.UserHomeDir()`.
- **Callers**: Directory creation with appropriate permissions. For example, `internal/statebackend` creates `SessionsDir` with `0700`.

## Design

This is a pure-domain package per `.claude/rules/logging-telemetry.md`. It:

- Does not import `log/slog` or `internal/telemetry` (pure-domain exemption).
- Is deterministic (given the same `projectDir` and environment, always returns the same `Paths`).
- Is single-threaded (no goroutines, no concurrent access).
- Achieves ~95% test coverage (table-driven tests with stdlib `testing` only).

The call site is responsible for logging the resolved paths and any I/O errors.
