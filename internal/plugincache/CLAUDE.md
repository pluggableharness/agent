# internal/plugincache — agent notes

- **This is a thin, synchronous path/stat layer.** The only real work is `filepath.Join` and `os.Stat`. No I/O-heavy operations or cross-process boundaries. Instrumentation is minimal per logging-telemetry.md: a single `slog.DebugContext` entry logging the resolved path on `Exists`, nothing more. The overhead of an OTel span would exceed the actual work being done.

- **No import of `internal/telemetry`.** Per the assessment above, a single `os.Stat` call does not warrant a span. The `DEBUG` log on the path is sufficient for troubleshooting.

- **Sanitization is deterministic, not cryptographic.** The sanitization scheme (replacing `/` with `_`) is simple, deterministic, and collision-resistant for the realistic input space (git-forge addresses). It is not intended to be a security boundary — it exists purely to make source addresses filesys-safe.

- **`Exists` distinguishes "not found" from "can't tell".** The function returns `(false, nil)` for `os.IsNotExist` (clear signal that the binary is not installed) and `(false, err)` for any other stat error (permission denied, etc.), allowing the caller to make an informed decision about how to proceed.

- **Platform string is built from `runtime.GOOS` and `runtime.GOARCH`.** No special handling — just a simple concatenation with `_` separator, matching the canonical platform key format used in lock files.
