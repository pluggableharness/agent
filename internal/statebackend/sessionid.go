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
	return newULID(t)
}

// NewEventID generates a new event ID as a ULID with the given timestamp —
// the stable, storage-independent identifier the events.id column holds
// (docs/specifications/state-backend.md#events), unique within a session's
// file. Same format and same generator as NewSessionID: canonical Crockford
// base32, uppercase, 26 characters, monotonic within a millisecond. Event
// IDs are caller-supplied — this package never mints one on the caller's
// behalf inside AppendEvent — so this is the one canonical way to produce
// one, not a second, subtly different generator (determinism.md).
//
// The returned ID sorts chronologically, but sorting event IDs is never how
// this package orders events: sequence is the sole ordering authority
// (determinism.md#ordering).
func NewEventID(t time.Time) string {
	return newULID(t)
}

// newULID is the single ULID generator behind both NewSessionID and
// NewEventID — one mutex, one monotonic entropy source, so IDs of either
// kind minted in the same millisecond can never collide with each other.
func newULID(t time.Time) string {
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
