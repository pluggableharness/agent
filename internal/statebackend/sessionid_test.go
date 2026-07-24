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

	for i := 0; i < goroutines; i++ {
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
	for i := 0; i < 10; i++ {
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
