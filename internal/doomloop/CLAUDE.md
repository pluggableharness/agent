# doomloop package

Pure domain logic: sliding-window hash detector for doom-loop detection (model repeating identical calls).

## What this package does

Tracks a fixed-size ring buffer of call hashes and reports when the most recent N consecutive hashes are identical. The kernel calls this at step 16 of every turn (see `docs/specifications/agent-loop/turn-algorithm.md`); if `Tripped()` returns true, the caller routes through the graceful-degradation path (inject a final-answer turn) rather than failing hard.

## No I/O, no logging, no telemetry

This is a pure domain calculation: the detector takes opaque hash strings, maintains a ring buffer, and performs pointer arithmetic. It does not:

- Compute hashes (caller provides them).
- Write logs or telemetry.
- Import `log/slog` or `internal/telemetry`.
- Perform I/O of any kind.

## Not goroutine-safe

The detector is designed for single-threaded use (one goroutine per session's turn loop) and does not include synchronization primitives. If a caller needs concurrent access, that caller is responsible for adding its own mutex or other coordination.

## Testing

Unit tests in `doomloop_test.go` cover:

- Configuration validation (threshold in [3, 5], window size >= threshold).
- Trip detection at exactly threshold consecutive identical hashes.
- Non-identical hashes breaking the run.
- Window eviction when exceeding window size.
- Reset clearing state.
- Multiple turns and multiple hashes per observe call.
- Edge cases (empty observe, window size exactly threshold).

Target: ~95% coverage for pure domain logic.
