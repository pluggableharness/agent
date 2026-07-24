package statebackend

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	// mu guards monotonic to ensure concurrent ULID generation produces unique IDs.
	mu sync.Mutex
	// monotonic is a ULID entropy source that produces monotonically increasing ULIDs
	// when called from the same millisecond, ensuring uniqueness across concurrent calls.
	monotonic = ulid.Monotonic(rand.Reader, 0)
)

// NewSessionID generates a new session ID as a ULID with the given timestamp.
// The ULID is in canonical Crockford base32 (uppercase, 26 characters),
// making session IDs sortable chronologically by filename alone.
func NewSessionID(t time.Time) string {
	mu.Lock()
	ms := ulid.Timestamp(t)
	id, _ := ulid.New(ms, monotonic)
	mu.Unlock()
	return id.String()
}

// ValidateSessionID returns an error if id is not a strictly valid canonical ULID.
// It rejects lowercase ULIDs, invalid characters, and values outside the ULID range.
func ValidateSessionID(id string) error {
	parsed, err := ulid.ParseStrict(id)
	if err != nil {
		return fmt.Errorf("statebackend: %w", err)
	}
	// ParseStrict validates the format; ensure round-trip matches (catches lowercase).
	if parsed.String() != id {
		return fmt.Errorf("statebackend: session id must be canonical uppercase")
	}
	return nil
}
