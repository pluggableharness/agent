package statebackend

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// FuzzNewEventID exercises NewEventID across arbitrary instants, asserting:
//  1. No panic for any representable time, including pre-epoch and
//     far-future instants whose millisecond value overflows a ULID's
//     48-bit timestamp field.
//  2. The result is always a 26-character canonical ULID — the events.id
//     format callers and ValidateSessionID's own strictness both assume.
func FuzzNewEventID(f *testing.F) {
	f.Add(int64(0))
	f.Add(time.Now().UnixNano())
	f.Add(int64(-1))
	f.Add(int64(1<<62 - 1))

	f.Fuzz(func(t *testing.T, nanos int64) {
		id := NewEventID(time.Unix(0, nanos))

		if len(id) != 26 {
			t.Fatalf("NewEventID(%d) = %q, length %d, want 26", nanos, id, len(id))
		}
		parsed, err := ulid.ParseStrict(id)
		if err != nil {
			t.Fatalf("NewEventID(%d) = %q, not a canonical ULID: %v", nanos, id, err)
		}
		if parsed.String() != id {
			t.Fatalf("NewEventID(%d) = %q, round-trip mismatch: %q", nanos, id, parsed.String())
		}
	})
}

// FuzzValidateSessionID exercises ValidateSessionID against arbitrary strings,
// asserting:
//  1. Any generated session ID must always validate with a nil error.
//  2. No panic on arbitrary attacker-controlled input.
//  3. If ValidateSessionID returns nil, ulid.ParseStrict must also succeed
//     and String() must round-trip exactly to the input.
func FuzzValidateSessionID(f *testing.F) {
	// Add seed examples.
	f.Add(NewSessionID(time.Now()))
	f.Add(NewSessionID(time.Unix(0, 0)))
	f.Add("")
	f.Add("invalid")
	f.Add("01ARZ3NDEKTSV4RRFFQ69G5FAV")

	f.Fuzz(func(t *testing.T, input string) {
		// Property 2: never panic on arbitrary input.
		err := ValidateSessionID(input)

		// Property 3: if validation succeeds, ensure round-trip consistency.
		if err == nil {
			parsed, parseErr := ulid.ParseStrict(input)
			if parseErr != nil {
				t.Fatalf("ValidateSessionID(%q) returned nil but ParseStrict failed: %v", input, parseErr)
			}
			if parsed.String() != input {
				t.Fatalf("ValidateSessionID(%q) succeeded but String() round-trip mismatch: %q", input, parsed.String())
			}
		}
	})
}
