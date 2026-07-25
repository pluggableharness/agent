package doomloop

import (
	"testing"
)

func TestNewValidThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       Config
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "default config",
			cfg:     DefaultConfig,
			wantErr: false,
		},
		{
			name:    "threshold 3 with window 8",
			cfg:     Config{WindowSize: 8, Threshold: 3},
			wantErr: false,
		},
		{
			name:    "threshold 4 with window 8",
			cfg:     Config{WindowSize: 8, Threshold: 4},
			wantErr: false,
		},
		{
			name:    "threshold 5 with window 8",
			cfg:     Config{WindowSize: 8, Threshold: 5},
			wantErr: false,
		},
		{
			name:    "threshold 3 with window 3",
			cfg:     Config{WindowSize: 3, Threshold: 3},
			wantErr: false,
		},
		{
			name:    "threshold 5 with window 100",
			cfg:     Config{WindowSize: 100, Threshold: 5},
			wantErr: false,
		},
		{
			name:      "threshold 2 is too low",
			cfg:       Config{WindowSize: 8, Threshold: 2},
			wantErr:   true,
			errSubstr: "outside range [3, 5]",
		},
		{
			name:      "threshold 6 is too high",
			cfg:       Config{WindowSize: 8, Threshold: 6},
			wantErr:   true,
			errSubstr: "outside range [3, 5]",
		},
		{
			name:      "threshold 0 is too low",
			cfg:       Config{WindowSize: 8, Threshold: 0},
			wantErr:   true,
			errSubstr: "outside range [3, 5]",
		},
		{
			name:      "threshold 10 is too high",
			cfg:       Config{WindowSize: 8, Threshold: 10},
			wantErr:   true,
			errSubstr: "outside range [3, 5]",
		},
		{
			name:      "window smaller than threshold",
			cfg:       Config{WindowSize: 2, Threshold: 3},
			wantErr:   true,
			errSubstr: "window size 2 must be >= threshold 3",
		},
		{
			name:      "window much smaller than threshold",
			cfg:       Config{WindowSize: 1, Threshold: 5},
			wantErr:   true,
			errSubstr: "window size 1 must be >= threshold 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, err := New(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("New() = %v, want error", err)
				}
				if tt.errSubstr != "" && !contains(err.Error(), tt.errSubstr) {
					t.Errorf("New() error %q does not contain %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("New() = %v, want nil error", err)
				}
				if d == nil {
					t.Errorf("New() returned nil detector with no error")
				}
			}
		})
	}
}

func TestTrippedWithZeroObservations(t *testing.T) {
	t.Parallel()

	d, err := New(DefaultConfig)
	if err != nil {
		t.Fatalf("New(DefaultConfig) = %v, want nil error", err)
	}

	if d.Tripped() {
		t.Errorf("Tripped() with no observations = true, want false")
	}
}

func TestTrippedWithFewerThanThresholdObservations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       Config
		numHashes int
	}{
		{
			name:      "threshold 3, 1 hash",
			cfg:       Config{WindowSize: 8, Threshold: 3},
			numHashes: 1,
		},
		{
			name:      "threshold 3, 2 hashes",
			cfg:       Config{WindowSize: 8, Threshold: 3},
			numHashes: 2,
		},
		{
			name:      "threshold 5, 4 hashes",
			cfg:       Config{WindowSize: 8, Threshold: 5},
			numHashes: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, err := New(tt.cfg)
			if err != nil {
				t.Fatalf("New() = %v, want nil error", err)
			}

			hashes := make([]string, tt.numHashes)
			for i := 0; i < tt.numHashes; i++ {
				hashes[i] = "same"
			}

			d.Observe(hashes)

			if d.Tripped() {
				t.Errorf("Tripped() with %d observations and threshold %d = true, want false",
					tt.numHashes, tt.cfg.Threshold)
			}
		})
	}
}

func TestTripsAtExactlyThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		threshold int
	}{
		{name: "threshold 3", threshold: 3},
		{name: "threshold 4", threshold: 4},
		{name: "threshold 5", threshold: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{WindowSize: 10, Threshold: tt.threshold}
			d, err := New(cfg)
			if err != nil {
				t.Fatalf("New() = %v, want nil error", err)
			}

			// Observe exactly threshold identical hashes
			hashes := make([]string, tt.threshold)
			for i := 0; i < tt.threshold; i++ {
				hashes[i] = "hash1"
			}

			d.Observe(hashes)

			if !d.Tripped() {
				t.Errorf("Tripped() with %d consecutive identical hashes (threshold %d) = false, want true",
					tt.threshold, tt.threshold)
			}
		})
	}
}

func TestNonIdenticalHashBreaksRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		threshold int
	}{
		{name: "threshold 3", threshold: 3},
		{name: "threshold 4", threshold: 4},
		{name: "threshold 5", threshold: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{WindowSize: 10, Threshold: tt.threshold}
			d, err := New(cfg)
			if err != nil {
				t.Fatalf("New() = %v, want nil error", err)
			}

			// Build a sequence: (threshold-1) identical, then one different
			hashes := make([]string, tt.threshold)
			for i := 0; i < tt.threshold-1; i++ {
				hashes[i] = "hash1"
			}
			hashes[tt.threshold-1] = "hash2"

			d.Observe(hashes)

			if d.Tripped() {
				t.Errorf("Tripped() with non-identical hash breaking the run = true, want false")
			}

			// Now add one more identical to the first set - still should not trip
			// At this point we have: [hash1, hash1, ..., hash1, hash2, hash1]
			// Last threshold entries are: hash1, hash2, hash1 - not identical
			d.Observe([]string{"hash1"})

			if d.Tripped() {
				t.Errorf("Tripped() after breaking the run and re-establishing one identical = true, want false")
			}

			// Need to add threshold-1 more identical hashes to form a new run of threshold
			// We already have 1 hash1 at the end (from previous observe)
			for i := 0; i < tt.threshold-1; i++ {
				d.Observe([]string{"hash1"})
			}

			// Now we should be tripped - last threshold entries are all hash1
			if !d.Tripped() {
				t.Errorf("Tripped() after re-establishing full run = false, want true")
			}
		})
	}
}

func TestSlidingWindowEviction(t *testing.T) {
	t.Parallel()

	cfg := Config{WindowSize: 5, Threshold: 3}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}

	// Add more than window size
	hashes := []string{"h1", "h2", "h3", "h4", "h5", "h6", "h7"}
	d.Observe(hashes)

	// Window should now have only the last 5: h3, h4, h5, h6, h7
	// Last 3 are h5, h6, h7 - not all identical
	if d.Tripped() {
		t.Errorf("Tripped() after window eviction = true, want false (last 3 are h5, h6, h7 - not all same)")
	}

	// Add more hashes to make the last 3 identical
	d.Observe([]string{"h8", "h8", "h8"})

	// Now the last 3 should be h8, h8, h8
	if !d.Tripped() {
		t.Errorf("Tripped() after adding identical tail = false, want true")
	}
}

func TestReset(t *testing.T) {
	t.Parallel()

	d, err := New(DefaultConfig)
	if err != nil {
		t.Fatalf("New(DefaultConfig) = %v, want nil error", err)
	}

	// Build a trip state
	hashes := []string{"same", "same", "same"}
	d.Observe(hashes)

	if !d.Tripped() {
		t.Errorf("Tripped() before reset = false, want true")
	}

	// Reset
	d.Reset()

	if d.Tripped() {
		t.Errorf("Tripped() after reset = true, want false")
	}

	// Add fewer than threshold and verify still not tripped
	d.Observe([]string{"hash1"})
	if d.Tripped() {
		t.Errorf("Tripped() with single observation after reset = true, want false")
	}
}

func TestMultipleTurns(t *testing.T) {
	t.Parallel()

	cfg := Config{WindowSize: 8, Threshold: 3}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}

	// Turn 1: observe 2 different hashes
	d.Observe([]string{"hash1", "hash2"})
	if d.Tripped() {
		t.Errorf("Turn 1: Tripped() = true, want false")
	}

	// Turn 2: observe 1 hash that doesn't match
	d.Observe([]string{"hash3"})
	if d.Tripped() {
		t.Errorf("Turn 2: Tripped() = true, want false")
	}

	// Turn 3: observe 2 identical hashes
	d.Observe([]string{"hash4", "hash4"})
	if d.Tripped() {
		t.Errorf("Turn 3: Tripped() = true, want false (only 2 consecutive hash4)")
	}

	// Turn 4: observe 1 more identical hash (now 3 consecutive)
	d.Observe([]string{"hash4"})
	if !d.Tripped() {
		t.Errorf("Turn 4: Tripped() = false, want true (3 consecutive hash4)")
	}

	// Turn 5: observe different hash
	d.Observe([]string{"hash5"})
	if d.Tripped() {
		t.Errorf("Turn 5: Tripped() = true, want false (broke the run)")
	}
}

func TestMultipleHashesPerObserve(t *testing.T) {
	t.Parallel()

	cfg := Config{WindowSize: 10, Threshold: 3}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}

	// Observe multiple hashes at once
	d.Observe([]string{"a", "b", "c"})

	// First 3 are a, b, c - not identical, so not tripped
	if d.Tripped() {
		t.Errorf("Tripped() with non-identical hashes = true, want false")
	}

	// Observe identical hashes
	d.Observe([]string{"x", "x", "x"})

	// Last 3 should be x, x, x
	if !d.Tripped() {
		t.Errorf("Tripped() with 3 identical x's = false, want true")
	}

	// Observe one different hash
	d.Observe([]string{"y"})

	// Should break the run
	if d.Tripped() {
		t.Errorf("Tripped() after adding different hash = true, want false")
	}
}

func TestEmptyObserve(t *testing.T) {
	t.Parallel()

	d, err := New(DefaultConfig)
	if err != nil {
		t.Fatalf("New(DefaultConfig) = %v, want nil error", err)
	}

	// Observe empty list shouldn't affect state
	d.Observe([]string{})
	if d.Tripped() {
		t.Errorf("Tripped() after empty observe = true, want false")
	}

	// Add actual hashes
	d.Observe([]string{"same", "same", "same"})
	if !d.Tripped() {
		t.Errorf("Tripped() after adding identical hashes = false, want true")
	}

	// Empty observe shouldn't affect the tripped state
	d.Observe([]string{})
	if !d.Tripped() {
		t.Errorf("Tripped() after another empty observe = false, want true")
	}
}

func TestWindowSizeExactlyThreshold(t *testing.T) {
	t.Parallel()

	cfg := Config{WindowSize: 3, Threshold: 3}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}

	// Fill window exactly
	d.Observe([]string{"a", "a", "a"})
	if !d.Tripped() {
		t.Errorf("Tripped() with full window of identical = false, want true")
	}

	// Add one more to trigger eviction
	d.Observe([]string{"b"})

	// Window now has a, a, b - not all identical
	if d.Tripped() {
		t.Errorf("Tripped() after eviction = true, want false")
	}

	// Add two more b's to make last 3 all b
	d.Observe([]string{"b", "b"})

	// Window should be a, b, b, b (but size 3) -> b, b, b
	if !d.Tripped() {
		t.Errorf("Tripped() with new identical run = false, want true")
	}
}

func TestConsecutiveIdsNotRequired(t *testing.T) {
	t.Parallel()

	cfg := Config{WindowSize: 10, Threshold: 3}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}

	// Build sequence: X, Y, Z, X, X, X
	// The last 3 are identical, even though there are other X's earlier
	d.Observe([]string{"X", "Y", "Z", "X", "X", "X"})

	if !d.Tripped() {
		t.Errorf("Tripped() with identical tail = false, want true")
	}

	// Add one more non-identical hash
	d.Observe([]string{"W"})

	if d.Tripped() {
		t.Errorf("Tripped() after breaking the run = true, want false")
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestConcurrentUnsafe is a reminder that the detector is not goroutine-safe.
// This test does NOT use t.Parallel() to document this behavior.
func TestConcurrentUnsafe(t *testing.T) {
	// This test just documents that we don't guarantee goroutine safety.
	// A real concurrent test would need synchronization outside the detector.
	d, err := New(DefaultConfig)
	if err != nil {
		t.Fatalf("New(DefaultConfig) = %v, want nil error", err)
	}

	// Just verify basic operations work
	d.Observe([]string{"a"})
	d.Observe([]string{"b"})
	if d.Tripped() {
		t.Errorf("Expected not tripped after 2 distinct hashes")
	}
}
