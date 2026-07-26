package statebackend

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func TestNewSessionID(t *testing.T) {
	t.Parallel()
	now := time.Now()

	tests := []struct {
		name string
		t    time.Time
	}{
		{"zero time", time.Time{}},
		{"unix epoch", time.Unix(0, 0)},
		{"now", now},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id := NewSessionID(tt.t)

			// Must be exactly 26 characters.
			if len(id) != 26 {
				t.Errorf("NewSessionID length = %d, want 26", len(id))
			}

			// Must be uppercase Crockford base32 only.
			if !regexp.MustCompile(`^[0-7][0-9A-Z]{25}$`).MatchString(id) {
				t.Errorf("NewSessionID format invalid: %q", id)
			}
		})
	}
}

// TestNewULIDOutOfRangeTimeStaysUniqueAndNonZero covers the timestamps
// ulid.New rejects with ErrBigTime — notably the zero time.Time, whose
// negative Unix seconds ulid.Timestamp wraps into an enormous uint64.
//
// The error used to be discarded, which left the returned ULID at its ZERO
// value: "00000000000000000000000000". That is a structurally valid
// canonical ULID, so ValidateSessionID accepts it and the existing format
// assertions passed — but every call for such a time produced the SAME id,
// which for an events.id primary key means a silent collision rather than
// a loud failure. Clamping keeps the ids distinct.
func TestNewULIDOutOfRangeTimeStaysUniqueAndNonZero(t *testing.T) {
	t.Parallel()

	const zeroULID = "00000000000000000000000000"

	tests := []struct {
		name string
		t    time.Time
	}{
		{"zero time", time.Time{}},
		{"far past", time.Date(-5000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"far future", time.Date(400000, 1, 1, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := NewSessionID(tt.t)
			second := NewSessionID(tt.t)

			if first == zeroULID || second == zeroULID {
				t.Fatalf("NewSessionID(%v) returned the zero ULID; an unrepresentable timestamp must still yield a real id", tt.t)
			}
			if first == second {
				t.Errorf("NewSessionID(%v) returned %q twice; ids for one timestamp must still be unique", tt.t, first)
			}
			if err := ValidateSessionID(first); err != nil {
				t.Errorf("ValidateSessionID(%q): %v", first, err)
			}
		})
	}
}

func TestNewSessionIDChronologicalOrder(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Second)
	t3 := t2.Add(1 * time.Second)

	id1 := NewSessionID(t1)
	id2 := NewSessionID(t2)
	id3 := NewSessionID(t3)

	ids := []string{id3, id1, id2}
	sort.Strings(ids)

	if ids[0] != id1 || ids[1] != id2 || ids[2] != id3 {
		t.Errorf("Chronological sort failed: %v", ids)
	}
}

func TestNewSessionIDConcurrentUniqueness(t *testing.T) {
	t.Parallel()

	const goroutines = 100
	ids := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	now := time.Now()

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			ids[idx] = NewSessionID(now)
		}(i)
	}
	wg.Wait()

	// All IDs must be unique.
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("Duplicate ID generated: %q", id)
			break
		}
		seen[id] = true
	}
}

// TestNewEventID mirrors TestNewSessionID: same generator, so the same
// format guarantee (26-character canonical uppercase Crockford base32).
func TestNewEventID(t *testing.T) {
	t.Parallel()
	now := time.Now()

	tests := []struct {
		name string
		t    time.Time
	}{
		{"zero time", time.Time{}},
		{"unix epoch", time.Unix(0, 0)},
		{"now", now},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id := NewEventID(tt.t)

			if len(id) != 26 {
				t.Errorf("NewEventID length = %d, want 26", len(id))
			}
			if !regexp.MustCompile(`^[0-7][0-9A-Z]{25}$`).MatchString(id) {
				t.Errorf("NewEventID format invalid: %q", id)
			}
			if _, err := ulid.ParseStrict(id); err != nil {
				t.Errorf("NewEventID(%v) = %q, not a canonical ULID: %v", tt.t, id, err)
			}
		})
	}
}

func TestNewEventIDChronologicalOrder(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Second)
	t3 := t2.Add(1 * time.Second)

	id1 := NewEventID(t1)
	id2 := NewEventID(t2)
	id3 := NewEventID(t3)

	ids := []string{id3, id1, id2}
	sort.Strings(ids)

	if ids[0] != id1 || ids[1] != id2 || ids[2] != id3 {
		t.Errorf("Chronological sort failed: %v", ids)
	}
}

func TestNewEventIDConcurrentUniqueness(t *testing.T) {
	t.Parallel()

	const goroutines = 100
	ids := make([]string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	now := time.Now()

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			ids[idx] = NewEventID(now)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("Duplicate ID generated: %q", id)
			break
		}
		seen[id] = true
	}
}

// TestNewEventIDDistinctFromSessionID asserts the two generators share one
// monotonic entropy source rather than being two independent generators
// that could mint the same value in the same millisecond.
func TestNewEventIDDistinctFromSessionID(t *testing.T) {
	t.Parallel()

	now := time.Now()
	seen := make(map[string]bool, 200)
	for range 100 {
		for _, id := range []string{NewSessionID(now), NewEventID(now)} {
			if seen[id] {
				t.Fatalf("NewEventID and NewSessionID collided on %q", id)
			}
			seen[id] = true
		}
	}
}

func TestValidateSessionID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"generated id is valid", NewSessionID(time.Now()), false},
		{"empty string", "", true},
		{"too short", "0123456789", true},
		{"too long", "0123456789ABCDEFGHIJKLMNOPQRST", true},
		{"lowercase rejected", strings.ToLower(NewSessionID(time.Now())), true},
		{"invalid chars", "01234567890ABCDEFGHIJKLMNO!!!", true},
		{"overflow (8 prefix)", "8123456789ABCDEFGHIJKLMNOPQR", true},
		{"all zeros", "00000000000000000000000000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateSessionID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSessionID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSessionIDRoundTrip(t *testing.T) {
	t.Parallel()

	// Any generated ID must pass validation and round-trip.
	for range 10 {
		id := NewSessionID(time.Now())
		if err := ValidateSessionID(id); err != nil {
			t.Errorf("Generated ID failed validation: %q, %v", id, err)
		}

		// Ensure it round-trips through ulid.ParseStrict.
		parsed, err := ulid.ParseStrict(id)
		if err != nil {
			t.Errorf("Generated ID failed ParseStrict: %q, %v", id, err)
		}
		if parsed.String() != id {
			t.Errorf("Round-trip mismatch: %q -> %q", id, parsed.String())
		}
	}
}
