package bounds

import (
	"math"
	"sync"
	"testing"
	"time"

	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

func TestFiredStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    Fired
		want sessionv1.SessionStatus
	}{
		{"max turns", FiredMaxTurns, sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_TURNS},
		{"max cost", FiredMaxCostUSD, sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_BUDGET_USD},
		{"max wall clock", FiredMaxWallClock, sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_WALL_CLOCK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.f.Status(); got != tt.want {
				t.Fatalf("Status() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFiredStatusPanicsOnFiredNone(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Status() on FiredNone did not panic")
		}
	}()
	FiredNone.Status()
}

func TestFiredStatusPanicsOnUnknownValue(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Status() on an unknown Fired value did not panic")
		}
	}()
	Fired(99).Status()
}

func TestCheckEachDimensionIndependently(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		limits  Limits
		turns   int
		cost    float64
		elapsed time.Duration
		want    Fired
	}{
		{
			name:    "turns only",
			limits:  Limits{MaxTurns: 3},
			turns:   3,
			cost:    0,
			elapsed: 0,
			want:    FiredMaxTurns,
		},
		{
			name:    "turns under limit",
			limits:  Limits{MaxTurns: 3},
			turns:   2,
			cost:    0,
			elapsed: 0,
			want:    FiredNone,
		},
		{
			name:    "cost only",
			limits:  Limits{MaxCostUSD: 1.00},
			turns:   0,
			cost:    1.00,
			elapsed: 0,
			want:    FiredMaxCostUSD,
		},
		{
			name:    "cost under limit",
			limits:  Limits{MaxCostUSD: 1.00},
			turns:   0,
			cost:    0.50,
			elapsed: 0,
			want:    FiredNone,
		},
		{
			name:    "wall clock only",
			limits:  Limits{MaxWallClock: 10 * time.Second},
			turns:   0,
			cost:    0,
			elapsed: 10 * time.Second,
			want:    FiredMaxWallClock,
		},
		{
			name:    "wall clock under limit",
			limits:  Limits{MaxWallClock: 10 * time.Second},
			turns:   0,
			cost:    0,
			elapsed: 9 * time.Second,
			want:    FiredNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := NewTracker(tt.limits, nil)
			for range tt.turns {
				tr.ObserveTurn()
			}
			if tt.cost != 0 {
				tr.Debit(tt.cost)
			}
			if got := tr.Check(tt.elapsed); got != tt.want {
				t.Fatalf("Check() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllUnboundedNeverFires(t *testing.T) {
	t.Parallel()

	tr := NewTracker(Limits{}, nil) // zero value: everything unbounded
	for i := range 10_000 {
		tr.ObserveTurn()
		tr.Debit(1_000_000.00)
		if got := tr.Check(time.Duration(i) * time.Hour); got != FiredNone {
			t.Fatalf("iteration %d: Check() = %v, want FiredNone", i, got)
		}
	}
}

func TestZeroMaxTurnsAndMaxCostAreUnbounded(t *testing.T) {
	t.Parallel()

	// Regression test for the "zero means unset, not fire immediately"
	// convention: a fresh Tracker whose Limits carry Go's zero value for
	// MaxTurns/MaxCostUSD must not fire on turn 1 / the first debit.
	tr := NewTracker(Limits{MaxTurns: 0, MaxCostUSD: 0}, nil)
	tr.ObserveTurn()
	tr.Debit(0.01)
	if got := tr.Check(0); got != FiredNone {
		t.Fatalf("Check() = %v, want FiredNone (zero bounds must be unbounded, not fire-immediately)", got)
	}
}

func TestCheckTieBreakPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limits Limits
		turns  int
		cost   float64
		wall   time.Duration
		want   Fired
	}{
		{
			name:   "all three fire: turns wins",
			limits: Limits{MaxTurns: 1, MaxCostUSD: 1.00, MaxWallClock: time.Second},
			turns:  1,
			cost:   1.00,
			wall:   time.Second,
			want:   FiredMaxTurns,
		},
		{
			name:   "cost and wall clock fire, turns doesn't: cost wins",
			limits: Limits{MaxTurns: 5, MaxCostUSD: 1.00, MaxWallClock: time.Second},
			turns:  1,
			cost:   1.00,
			wall:   time.Second,
			want:   FiredMaxCostUSD,
		},
		{
			name:   "only wall clock fires",
			limits: Limits{MaxTurns: 5, MaxCostUSD: 1.00, MaxWallClock: time.Second},
			turns:  1,
			cost:   0.10,
			wall:   time.Second,
			want:   FiredMaxWallClock,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := NewTracker(tt.limits, nil)
			for range tt.turns {
				tr.ObserveTurn()
			}
			tr.Debit(tt.cost)
			if got := tr.Check(tt.wall); got != tt.want {
				t.Fatalf("Check() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemainingCostUSDUnboundedSentinel(t *testing.T) {
	t.Parallel()

	tr := NewTracker(Limits{}, nil)
	if got := tr.RemainingCostUSD(); got != unbounded {
		t.Fatalf("RemainingCostUSD() = %v, want unbounded sentinel %v", got, unbounded)
	}
	tr.Debit(500.00)
	if got := tr.RemainingCostUSD(); got != unbounded {
		t.Fatalf("RemainingCostUSD() after spend = %v, want unbounded sentinel %v (unbounded budget never shrinks)", got, unbounded)
	}
}

func TestParentChainRollup(t *testing.T) {
	t.Parallel()

	grandparent := NewTracker(Limits{MaxCostUSD: 100.00}, nil)
	parent := NewTracker(Limits{MaxCostUSD: 100.00}, grandparent)
	child := NewTracker(Limits{MaxCostUSD: 100.00}, parent)

	child.Debit(10.00)
	child.Debit(5.00)

	for _, tr := range []struct {
		name string
		t    *Tracker
	}{
		{"child", child},
		{"parent", parent},
		{"grandparent", grandparent},
	} {
		if got := tr.t.TotalCostUSD(); got != 15.00 {
			t.Fatalf("%s.TotalCostUSD() = %v, want 15.00 (rollup from child's Debit)", tr.name, got)
		}
	}

	// Every level shares the same 100.00 budget, all with the same spend
	// rolled up, so remaining is 85.00 everywhere.
	for _, tr := range []struct {
		name string
		t    *Tracker
	}{
		{"child", child},
		{"parent", parent},
		{"grandparent", grandparent},
	} {
		if got := tr.t.RemainingCostUSD(); math.Abs(got-85.00) > 1e-9 {
			t.Fatalf("%s.RemainingCostUSD() = %v, want 85.00", tr.name, got)
		}
	}
}

func TestRemainingCostUSDReflectsAncestorsTighterBudget(t *testing.T) {
	t.Parallel()

	// Ancestor has a tight budget; child's own limit is generous. The
	// child's RemainingCostUSD must reflect the ancestor's tighter
	// constraint (min over the whole chain), per
	// turn-algorithm.md#cost-accounting and the identical reasoning
	// subagents.md#depth-limits establishes for max_depth.
	tightParent := NewTracker(Limits{MaxCostUSD: 1.00}, nil)
	generousChild := NewTracker(Limits{MaxCostUSD: 1_000_000.00}, tightParent)

	tightParent.Debit(0.90) // parent directly spends toward its own tight budget

	got := generousChild.RemainingCostUSD()
	want := 0.10 // parent's remaining: 1.00 - 0.90
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("generousChild.RemainingCostUSD() = %v, want %v (ancestor's tighter remaining budget)", got, want)
	}

	// Once the parent's tight budget is exhausted by more of the
	// parent's own spend, the child's Check must report FiredMaxCostUSD
	// even though the child's own limit is nowhere near exhausted.
	tightParent.Debit(0.20)
	if got := generousChild.Check(0); got != FiredMaxCostUSD {
		t.Fatalf("generousChild.Check() = %v, want FiredMaxCostUSD (ancestor exhausted)", got)
	}
}

func TestDebitRollsUpThroughMultipleLevels(t *testing.T) {
	t.Parallel()

	root := NewTracker(Limits{}, nil)
	mid := NewTracker(Limits{}, root)
	leaf := NewTracker(Limits{}, mid)

	leaf.Debit(1.23)
	mid.Debit(4.56) // a direct debit at a middle level must also roll up

	if got := leaf.TotalCostUSD(); got != 1.23 {
		t.Fatalf("leaf.TotalCostUSD() = %v, want 1.23", got)
	}
	if got := mid.TotalCostUSD(); math.Abs(got-(1.23+4.56)) > 1e-9 {
		t.Fatalf("mid.TotalCostUSD() = %v, want %v", got, 1.23+4.56)
	}
	if got := root.TotalCostUSD(); math.Abs(got-(1.23+4.56)) > 1e-9 {
		t.Fatalf("root.TotalCostUSD() = %v, want %v", got, 1.23+4.56)
	}
}

func TestConcurrentAccess(t *testing.T) {
	parent := NewTracker(Limits{MaxTurns: 1_000_000, MaxCostUSD: 1_000_000, MaxWallClock: time.Hour}, nil)
	tr := NewTracker(Limits{MaxTurns: 1_000_000, MaxCostUSD: 1_000_000, MaxWallClock: time.Hour}, parent)

	const goroutines = 50
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				tr.ObserveTurn()
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				tr.Debit(0.01)
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				tr.Check(time.Second)
				_ = tr.TotalCostUSD()
				_ = tr.RemainingCostUSD()
			}
		}()
	}
	wg.Wait()

	wantCost := float64(goroutines*iterations) * 0.01
	if got := tr.TotalCostUSD(); math.Abs(got-wantCost) > 1e-6 {
		t.Fatalf("TotalCostUSD() = %v, want %v", got, wantCost)
	}
	if got := parent.TotalCostUSD(); math.Abs(got-wantCost) > 1e-6 {
		t.Fatalf("parent.TotalCostUSD() = %v, want %v (rolled up)", got, wantCost)
	}
}

func TestNewTrackerRootHasNilParent(t *testing.T) {
	t.Parallel()

	tr := NewTracker(Limits{MaxCostUSD: 5.00}, nil)
	tr.Debit(1.00)
	if got := tr.RemainingCostUSD(); math.Abs(got-4.00) > 1e-9 {
		t.Fatalf("RemainingCostUSD() = %v, want 4.00", got)
	}
}
