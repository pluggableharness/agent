# internal/xdg

## Exemption from logging/telemetry

This is a pure-domain package per `.claude/rules/logging-telemetry.md`. It MUST NOT import `log/slog` or `internal/telemetry`. The package is:

- **I/O-free** except for `os.UserHomeDir()` to resolve `$HOME` when XDG env vars need defaults.
- **Deterministic** — same inputs always produce the same output.
- **Single-threaded** — no goroutines or concurrent access.
- **~95% test-covered** — all logic covered by table-driven tests using stdlib `testing` only.

Call sites are responsible for logging and instrumentation around `Resolve()`'s result.

## Testing strategy

- **Table-driven**: Each test uses subtests with `t.Parallel()` to enable parallelism.
- **Env var isolation**: Each subtest that mutates XDG env vars uses `t.Setenv()`, which auto-restores per subtest.
- **Coverage targets**: Explicit XDG vars, unset XDG vars (fallback paths), relative and absolute project dirs, exact suffix paths.
- **No mocking**: All tests use real `t.TempDir()` for filesystem paths, no fakes or mocks needed.

## Notes for reviewers

- `getenvOrDefault()` is a private helper and tested via its callers (each XDG var path).
- Relative project dirs are supported (e.g., `.`, `./subdir`) — not just absolute paths. They stay relative in the resolved paths (no normalization to absolute).
- `os.UserHomeDir()` is called only once per `Resolve()` call, only when needed for XDG fallback defaults. If all XDG env vars are set, `os.UserHomeDir()` is never called.
