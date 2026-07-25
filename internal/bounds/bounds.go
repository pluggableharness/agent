package bounds

import (
	"math"
	"sync"
	"time"

	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// Limits is the three independent bound dimensions
// (turn-algorithm.md#independent-bound-dimensions). A zero value in any
// field means that dimension is unbounded — never "fire immediately". This
// resolves a real ambiguity: a literal zero bound would otherwise fire
// before the first turn, which is never the intended meaning of an omitted
// HCL field defaulting to Go's zero value.
type Limits struct {
	// MaxTurns is the maximum number of turns a session may run. Zero
	// means unbounded.
	MaxTurns int
	// MaxCostUSD is the maximum cumulative spend, in US dollars, a
	// session (and its descendants, once rolled up) may accrue. Zero
	// means unbounded.
	MaxCostUSD float64
	// MaxWallClock is the maximum wall-clock duration a session may run
	// for. Zero means unbounded.
	MaxWallClock time.Duration
}

// unbounded is the sentinel returned in place of +Inf for an unbounded
// remaining budget — math.MaxFloat64 is large enough that no realistic
// cost figure ever approaches it, while remaining an ordinary finite
// float64 usable in comparisons without special-casing infinities.
const unbounded = math.MaxFloat64

// Fired identifies which bound dimension (if any) has fired.
type Fired int

const (
	// FiredNone means no bound has fired.
	FiredNone Fired = iota
	// FiredMaxTurns means the session's max_turns bound fired.
	FiredMaxTurns
	// FiredMaxCostUSD means the session's max_cost_usd bound fired,
	// possibly due to spend rolled up from a descendant session or an
	// ancestor's tighter budget.
	FiredMaxCostUSD
	// FiredMaxWallClock means the session's max_wall_clock_s bound fired.
	FiredMaxWallClock
)

// Status maps a fired bound to its terminal session status, per
// turn-algorithm.md#limit-reached-behavior's three named status subtypes.
// Status MUST NOT be called when f is FiredNone — there is no sensible
// terminal status for "nothing fired," and calling it in that case is a
// caller bug; Status panics rather than returning a misleading zero value
// that could be mistaken for a real status.
func (f Fired) Status() sessionv1.SessionStatus {
	switch f {
	case FiredMaxTurns:
		return sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_TURNS
	case FiredMaxCostUSD:
		return sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_BUDGET_USD
	case FiredMaxWallClock:
		return sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_WALL_CLOCK
	case FiredNone:
		panic("bounds: Status called with FiredNone")
	default:
		panic("bounds: Status called with unknown Fired value")
	}
}

// Tracker tracks one session's bound state: turns observed, cost spent, and
// (via Check) elapsed wall-clock. It is safe for concurrent use —
// ObserveTurn, Debit, and Check may all be called from goroutines running
// concurrent tool calls within one turn (turn-algorithm.md's step 9's
// concurrent data-source execution).
type Tracker struct {
	mu sync.Mutex

	limits  Limits
	turns   int
	costUSD float64
	parent  *Tracker
}

// NewTracker returns a tracker for one session. parent is nil for a root
// session — this build's only production case, since the kernel does not
// yet support sub-agent spawning. When a session tree exists, a child's
// tracker is constructed with its parent, and Debit walks the chain to the
// root per turn-algorithm.md#cost-accounting's "atomically subtracted at
// every session on the path" requirement — this constructor and Debit's
// walk are built correctly now specifically so nothing here changes when
// that lands.
func NewTracker(l Limits, parent *Tracker) *Tracker {
	return &Tracker{limits: l, parent: parent}
}

// ObserveTurn increments this session's turn counter by one.
func (t *Tracker) ObserveTurn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns++
}

// Debit records usd of spend against this session AND walks parent (and
// parent's parent, etc.) subtracting the same usd from each ancestor's
// running total — turn-algorithm.md#cost-accounting's rollup rule.
//
// Lock ordering: Debit takes and releases each tracker's own mutex in
// strict child-to-root order, one at a time, never holding two mutexes in
// the chain simultaneously — the one lock-ordering rule in this package.
// A future caller adding cross-tracker logic to this walk must preserve
// that "child's mutex fully released before the parent's is taken"
// property; acquiring an ancestor's mutex while still holding a
// descendant's would invert the order relative to any concurrent walk
// starting further up the chain and risk deadlock.
func (t *Tracker) Debit(usd float64) {
	for cur := t; cur != nil; cur = cur.parent {
		cur.mu.Lock()
		cur.costUSD += usd
		cur.mu.Unlock()
	}
}

// TotalCostUSD returns this session's own accumulated spend: every dollar
// Debited directly against this Tracker, plus whatever descendants below it
// in the session tree have rolled up through it via their own Debit calls.
func (t *Tracker) TotalCostUSD() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.costUSD
}

// RemainingCostUSD returns this session's own remaining cost budget:
// Limits.MaxCostUSD minus TotalCostUSD(), clamped to the ancestor chain's
// tightest remaining budget (min over the whole chain from this session up
// to the root) — an ancestor's tighter budget always wins going down,
// mirroring the identical reasoning
// agent-loop/subagents.md#depth-limits already establishes for max_depth.
// Returns the unbounded sentinel (math.MaxFloat64) when Limits.MaxCostUSD
// is 0 (unbounded) and every ancestor is also unbounded.
func (t *Tracker) RemainingCostUSD() float64 {
	remaining := unbounded
	for cur := t; cur != nil; cur = cur.parent {
		cur.mu.Lock()
		own := unbounded
		if cur.limits.MaxCostUSD != 0 {
			own = cur.limits.MaxCostUSD - cur.costUSD
		}
		cur.mu.Unlock()
		if own < remaining {
			remaining = own
		}
	}
	return remaining
}

// Check evaluates all three bound dimensions given elapsed (the session's
// wall-clock duration so far, computed by the caller — this package never
// reads a clock itself, keeping it a pure function of its inputs) and
// returns which one fired, if any. Each dimension is checked independently;
// if more than one would fire simultaneously, they are returned in this
// priority order: FiredMaxTurns, then FiredMaxCostUSD, then
// FiredMaxWallClock. This ordering is an arbitrary but deterministic
// tie-break — turns first because a turn-count bound is typically the
// tightest and cheapest to have already incremented, cost next because it
// can reflect rolled-up descendant spend the caller may want surfaced ahead
// of a merely-elapsed clock, wall-clock last.
func (t *Tracker) Check(elapsed time.Duration) Fired {
	t.mu.Lock()
	turns := t.turns
	maxTurns := t.limits.MaxTurns
	maxWallClock := t.limits.MaxWallClock
	t.mu.Unlock()

	if maxTurns != 0 && turns >= maxTurns {
		return FiredMaxTurns
	}
	if t.RemainingCostUSD() <= 0 {
		return FiredMaxCostUSD
	}
	if maxWallClock != 0 && elapsed >= maxWallClock {
		return FiredMaxWallClock
	}
	return FiredNone
}
