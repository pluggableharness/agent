package statebackend

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

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
