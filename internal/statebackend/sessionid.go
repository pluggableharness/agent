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
	ms := clampULIDTime(t)

	mu.Lock()
	defer mu.Unlock()

	id, err := ulid.New(ms, monotonic)
	if err != nil {
		// ms is already range-clamped, so the only error left is
		// ulid.ErrMonotonicOverflow: the monotonic source's 80-bit entropy
		// space for THIS millisecond is exhausted, which takes on the
		// order of 2^40 ids inside one millisecond. Falling back to plain
		// random entropy gives up only the within-millisecond monotonic
		// ordering property — never an ordering authority here, since
		// sequence is (.claude/rules/determinism.md) — and cannot itself
		// fail, because crypto/rand does not return errors as of Go 1.24.
		//
		// Handled rather than discarded because the discarded-error form
		// left id at its ZERO value: a perfectly valid canonical ULID that
		// ValidateSessionID accepts, identical on every occurrence, which
		// would enter the audit log as a duplicate event identity.
		id = ulid.MustNew(ms, rand.Reader)
	}
	return id.String()
}

// clampULIDTime converts t to the millisecond count ulid.New takes,
// clamping it into the range a ULID can represent so ulid.New can never
// fail with ulid.ErrBigTime.
//
// Clamping rather than erroring keeps newULID's signature infallible for
// its callers, who mint ids inline while building an event and have no
// sensible recovery from "that timestamp is unrepresentable". Both ends
// are saturating: a pre-epoch time (notably the zero time.Time, which
// ulid.Timestamp would otherwise wrap into an enormous uint64) floors to
// the ULID epoch, and a far-future time saturates at ulid.MaxTime. The
// resulting id is still unique — entropy, not the timestamp, is what
// makes it so — which is the property that matters, since sequence and
// not id ordering is this package's ordering authority.
func clampULIDTime(t time.Time) uint64 {
	msi := t.UnixMilli()
	if msi < 0 {
		return 0
	}
	if uint64(msi) > ulid.MaxTime() {
		return ulid.MaxTime()
	}
	return uint64(msi)
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
