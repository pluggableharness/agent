# doomloop

`doomloop` implements the kernel-owned sliding-window hash detector for catching a model stuck repeating a functionally identical tool call.

## Overview

The detector maintains a sliding window of recent resource/data-source call hashes and reports when the most recent `Threshold` consecutive hashes are all identical. When `Tripped()` returns true, the caller routes this through the graceful-degradation path (injecting a final-answer turn) rather than a raw exception.

## Configuration

The detector is configured with two parameters:

- **`WindowSize`**: The total number of hashes to keep in the sliding window (e.g., 8). Must be >= `Threshold`.
- **`Threshold`**: The number of consecutive identical hashes required to trigger (e.g., 3). Must be in the range [3, 5] and is configurable per session.

`DefaultConfig` provides the canonical defaults: window size 8, threshold 3.

## Usage

```go
cfg := doomloop.Config{WindowSize: 8, Threshold: 3}
detector, err := doomloop.New(cfg)
if err != nil {
    // Handle invalid config
}

// Each turn, observe the hashes of that turn's calls in declaration order
detector.Observe([]string{hash1, hash2, hash3})

// Check if the detector has tripped
if detector.Tripped() {
    // Route through graceful-degradation (final-answer turn)
    detector.Reset() // Clear state for the recovery turn
}
```

## Semantics

- `Observe(hashes)` appends each hash in `hashes` to the window in order, evicting the oldest entries if the window exceeds `WindowSize`.
- `Tripped()` returns true if and only if the most recent `Threshold` entries in the window are all identical.
- A single non-identical hash within that span causes `Tripped()` to return false; fewer than `Threshold` entries observed so far also returns false.
- `Reset()` clears the window and resets the trip state, typically called after routing a trip through the caller's limit-reached path.

## Implementation notes

- The detector is **not goroutine-safe** and is designed for single-threaded use (one goroutine per session's turn loop).
- The detector takes opaque hash strings and does not compute hashes itself — the caller is responsible for computing hashes via `internal/callhash` or equivalent.
- The detector is a pure domain package with no I/O, no logging, and no external dependencies beyond the standard library.
