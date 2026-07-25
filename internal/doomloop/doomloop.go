package doomloop

import (
	"errors"
	"fmt"
)

// Config is the doom-loop detector's tunable window/threshold, per
// turn-algorithm.md#doom-loop-detection.
type Config struct {
	WindowSize int // MUST be >= Threshold
	Threshold  int // MUST be in [3, 5]
}

// DefaultConfig is the canonical default: window 8, threshold 3.
var DefaultConfig = Config{WindowSize: 8, Threshold: 3}

// ErrInvalidThreshold is returned by New when cfg.Threshold is outside
// [3, 5] or cfg.WindowSize < cfg.Threshold.
var ErrInvalidThreshold = errors.New("doom-loop: invalid threshold or window size")

// Detector tracks a sliding window of recent call hashes (produced by
// internal/callhash.Call — this package takes opaque strings and never
// computes a hash itself) and reports whether the most recent Threshold
// hashes are all identical.
type Detector struct {
	windowSize int
	threshold  int
	window     []string
}

// New validates cfg and returns a Detector. Returns ErrInvalidThreshold
// for an out-of-range Threshold or a WindowSize smaller than Threshold.
func New(cfg Config) (*Detector, error) {
	if cfg.Threshold < 3 || cfg.Threshold > 5 {
		return nil, fmt.Errorf("%w: threshold %d is outside range [3, 5]", ErrInvalidThreshold, cfg.Threshold)
	}
	if cfg.WindowSize < cfg.Threshold {
		return nil, fmt.Errorf("%w: window size %d must be >= threshold %d", ErrInvalidThreshold, cfg.WindowSize, cfg.Threshold)
	}
	return &Detector{
		windowSize: cfg.WindowSize,
		threshold:  cfg.Threshold,
		window:     make([]string, 0, cfg.WindowSize),
	}, nil
}

// Observe records one turn's resource/data-source call hashes, in
// declaration order, appending them to the sliding window (evicting the
// oldest entries once the window exceeds WindowSize).
func (d *Detector) Observe(hashes []string) {
	for _, hash := range hashes {
		d.window = append(d.window, hash)
		// Evict oldest entries once we exceed window size
		if len(d.window) > d.windowSize {
			d.window = d.window[1:]
		}
	}
}

// Tripped reports whether the most recent Threshold entries in the
// window are all identical (a non-identical hash within that span means
// not tripped; fewer than Threshold entries observed so far also means
// not tripped).
func (d *Detector) Tripped() bool {
	// Not enough entries yet
	if len(d.window) < d.threshold {
		return false
	}

	// Get the most recent threshold hashes
	startIdx := len(d.window) - d.threshold

	// Check if all recent threshold hashes are identical
	first := d.window[startIdx]
	for i := startIdx + 1; i < len(d.window); i++ {
		if d.window[i] != first {
			return false
		}
	}

	return true
}

// Reset clears the window, e.g. after routing a trip through the
// caller's limit-reached path.
func (d *Detector) Reset() {
	d.window = d.window[:0]
}
