package sessionstate

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// Live is the sole writer for one session's sqlite file
// (docs/specifications/state-backend.md#ordering--concurrency). One Live
// wraps exactly one *statebackend.Session, opened or created by whatever
// caller is standing the session up (a future turn/session driver), and
// owns that session's in-memory budget tracker
// (docs/specifications/state-backend.md#live-vs-post-hoc-tree-walking:
// budget state is live, in-memory, and never persisted).
//
// The zero value is not usable — construct with NewLive. Safe for
// concurrent use: mu serializes every Emit/EmitMessage/EmitPlan call so a
// session's append-then-republish sequence never interleaves with another
// call on the same Live.
type Live struct {
	mu      sync.Mutex
	session *statebackend.Session
	bus     *eventbus.Bus
	clock   func() time.Time
	budget  *bounds.Tracker
	logger  *slog.Logger
	telem   *telemetry.Provider
	id      string // this session's id, for logging/attribution
}

// defaultTelemetryProvider builds the Provider a Live falls back to when
// NewLive is called with a nil telem — every signal disabled, matching
// internal/statebackend's and internal/eventbus's own fallback so a caller
// that doesn't care about telemetry doesn't have to construct a Provider
// just to satisfy this package's constructor.
func defaultTelemetryProvider() (*telemetry.Provider, error) {
	return telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
}

// NewLive wraps an already-created/opened *statebackend.Session as the
// sole writer for its file, with bus republish and budget tracking wired
// in. parent is the parent session's *bounds.Tracker for cost rollup — nil
// for a root session, this build's only production case
// (internal/bounds's own doc comment on why the parent-link seam exists
// even though nothing currently uses it non-nil). clock defaults to
// time.Now, telem to a Provider with every signal disabled, and logger to
// slog.Default() when passed nil — the same fallback convention
// internal/statebackend and internal/eventbus already use, so a caller
// that only needs the mechanics doesn't have to construct every optional
// dependency by hand.
func NewLive(session *statebackend.Session, bus *eventbus.Bus, limits bounds.Limits, parentBudget *bounds.Tracker, clock func() time.Time, telem *telemetry.Provider, logger *slog.Logger) *Live {
	if clock == nil {
		clock = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	if telem == nil {
		// Unreachable in practice: defaultTelemetryProvider's
		// telemetry.Config{} is a fixed, valid zero value this package
		// controls end to end, the same reasoning internal/eventbus.New
		// gives for panicking here rather than threading an error through
		// a constructor every other caller in this codebase expects to be
		// infallible given no required arguments.
		prov, err := defaultTelemetryProvider()
		if err != nil {
			panic(err)
		}
		telem = prov
	}

	return &Live{
		session: session,
		bus:     bus,
		clock:   clock,
		budget:  bounds.NewTracker(limits, parentBudget),
		logger:  logger,
		telem:   telem,
		id:      session.ID(),
	}
}

// Budget exposes this session's *bounds.Tracker for a caller (the future
// turn/session driver) to check/debit directly, rather than duplicating
// budget-tracking logic in this package.
func (l *Live) Budget() *bounds.Tracker {
	return l.budget
}

// There is deliberately NO accessor exposing the wrapped
// *statebackend.Session.
//
// One existed, so the composition root could hand the kernel's turn-stack
// collaborators the same open handle rather than a second one on the same
// file. It was removed because it defeated the two properties this type
// exists to provide: every event written through the raw handle skipped
// both mu (so a session no longer had one writer at a time) and the
// kernel.event.{kind} republish (so no kernel-originated event ever
// reached the bus at all). AppendEvent/AppendMessage/AppendPlan in emit.go
// are the supported way to hand a caller that same session — they take the
// caller's own already-built statebackend.Event, so nothing is lost by
// going through them. Don't reintroduce the accessor.

// Close closes the underlying statebackend.Session.
func (l *Live) Close() error {
	return l.session.Close()
}
