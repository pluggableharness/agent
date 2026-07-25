package circuitbreaker

import (
	"fmt"
	"sync"
	"testing"
)

// event is one step in a scripted sequence of Breaker calls against a
// single provider, used by the table-driven tests below.
type event struct {
	kind        string // "denial", "crash", "success", or "reset"
	wantTripped bool   // ignored for "reset"
}

// runScript replays events against b for a fixed provider name ("p1") —
// every table-driven test below exercises a single provider's counters, so
// the provider name is not itself a table dimension.
func runScript(t *testing.T, b *Breaker, events []event) {
	t.Helper()
	const provider = "p1"
	for i, ev := range events {
		var got bool
		switch ev.kind {
		case "denial":
			got = b.RecordDenial(provider)
		case "crash":
			got = b.RecordCrash(provider)
		case "success":
			b.RecordSuccess(provider)
			continue
		case "reset":
			b.Reset(provider)
			continue
		default:
			t.Fatalf("step %d: unknown event kind %q", i, ev.kind)
		}
		if got != ev.wantTripped {
			t.Fatalf("step %d (%s): tripped = %v, want %v", i, ev.kind, got, ev.wantTripped)
		}
	}
}

func TestConsecutiveOnly(t *testing.T) {
	t.Parallel()

	newBreaker := func() *Breaker {
		return New(Config{ConsecutiveThreshold: 3})
	}

	t.Run("N-1 denials do not trip, Nth trips", func(t *testing.T) {
		t.Parallel()
		b := newBreaker()
		runScript(t, b, []event{
			{"denial", false},
			{"denial", false},
			{"denial", true},
		})
	})

	t.Run("success resets the consecutive count", func(t *testing.T) {
		t.Parallel()
		b := newBreaker()
		runScript(t, b, []event{
			{"denial", false},
			{"denial", false},
			{"success", false},
			// fresh streak: takes a full N again, not just one more.
			{"denial", false},
			{"denial", false},
			{"denial", true},
		})
	})

	t.Run("window disabled via zero WindowThreshold never trips on its own", func(t *testing.T) {
		t.Parallel()
		b := New(Config{ConsecutiveThreshold: 0, WindowSize: 5, WindowThreshold: 0})
		runScript(t, b, []event{
			{"denial", false},
			{"denial", false},
			{"denial", false},
			{"denial", false},
			{"denial", false},
			{"denial", false},
			{"denial", false},
		})
	})

	t.Run("window disabled via zero WindowSize never trips on its own", func(t *testing.T) {
		t.Parallel()
		b := New(Config{ConsecutiveThreshold: 0, WindowSize: 0, WindowThreshold: 3})
		runScript(t, b, []event{
			{"denial", false},
			{"denial", false},
			{"denial", false},
			{"denial", false},
		})
	})
}

func TestWindowOnly(t *testing.T) {
	t.Parallel()

	t.Run("M bad events within trailing window need not be consecutive", func(t *testing.T) {
		t.Parallel()
		// WindowSize=5, WindowThreshold=3, ConsecutiveThreshold disabled.
		b := New(Config{WindowSize: 5, WindowThreshold: 3})
		runScript(t, b, []event{
			{"denial", false},  // window: [D]                    bad=1
			{"success", false}, // window: [D,S]                  bad=1
			{"crash", false},   // window: [D,S,C]                bad=2
			{"success", false}, // window: [D,S,C,S]              bad=2
			{"denial", true},   // window: [D,S,C,S,D]            bad=3 -> trip
		})
	})

	t.Run("bad events aging out of the window stop tripping", func(t *testing.T) {
		t.Parallel()
		b := New(Config{WindowSize: 3, WindowThreshold: 2})
		runScript(t, b, []event{
			{"denial", false}, // [D]        bad=1
			{"denial", true},  // [D,D]      bad=2 -> trip
			// three successes fully evict both denials from a size-3 window.
			{"success", false}, // [D,D,S]   bad=2 (still tripped-eligible, window not slid past yet)
			{"success", false}, // [D,S,S]   bad=1
			{"success", false}, // [S,S,S]   bad=0
			{"denial", false},  // [S,S,D]   bad=1, below threshold
		})
	})
}

func TestBothThresholdsIndependent(t *testing.T) {
	t.Parallel()

	t.Run("consecutive threshold trips first", func(t *testing.T) {
		t.Parallel()
		b := New(Config{ConsecutiveThreshold: 3, WindowSize: 10, WindowThreshold: 8})
		runScript(t, b, []event{
			{"denial", false},
			{"denial", false},
			{"denial", true}, // consecutive=3 trips; window bad=3 < 8
		})
	})

	t.Run("window threshold trips first", func(t *testing.T) {
		t.Parallel()
		// ConsecutiveThreshold high enough that only the window check can fire.
		b := New(Config{ConsecutiveThreshold: 10, WindowSize: 4, WindowThreshold: 2})
		runScript(t, b, []event{
			{"denial", false},
			{"success", false}, // breaks consecutive streak; window bad=1
			{"denial", true},   // window bad=2 >= 2 trips; consecutive=1 < 10
		})
	})
}

func TestBothThresholdsZeroNeverTrips(t *testing.T) {
	t.Parallel()
	b := New(Config{})
	for i := 0; i < 50; i++ {
		if got := b.RecordDenial("p1"); got {
			t.Fatalf("denial %d: tripped = true, want false", i)
		}
		if got := b.RecordCrash("p1"); got {
			t.Fatalf("crash %d: tripped = true, want false", i)
		}
	}
}

func TestPerProviderIsolation(t *testing.T) {
	t.Parallel()
	b := New(Config{ConsecutiveThreshold: 2})

	if got := b.RecordDenial("A"); got {
		t.Fatalf("A denial 1: tripped = true, want false")
	}
	// B is untouched by A's denials.
	if got := b.RecordDenial("B"); got {
		t.Fatalf("B denial 1: tripped = true, want false")
	}
	if got := b.RecordDenial("A"); !got {
		t.Fatalf("A denial 2: tripped = false, want true")
	}
	// B still needs its own second bad event.
	if got := b.RecordDenial("B"); !got {
		t.Fatalf("B denial 2: tripped = false, want true")
	}
}

func TestSharedSignalAcrossDenialAndCrash(t *testing.T) {
	t.Parallel()
	b := New(Config{ConsecutiveThreshold: 3})
	runScript(t, b, []event{
		{"denial", false},
		{"crash", false},
		{"denial", true}, // denial+crash+denial = 3 consecutive bad events, regardless of kind
	})
}

func TestReset(t *testing.T) {
	t.Parallel()
	b := New(Config{ConsecutiveThreshold: 2, WindowSize: 3, WindowThreshold: 2})

	if got := b.RecordDenial("p1"); got {
		t.Fatalf("denial 1: tripped = true, want false")
	}
	if got := b.RecordDenial("p1"); !got {
		t.Fatalf("denial 2: tripped = false, want true")
	}

	b.Reset("p1")

	// After Reset, a single bad event must not still read as tripped.
	if got := b.RecordDenial("p1"); got {
		t.Fatalf("post-reset denial 1: tripped = true, want false")
	}
	if got := b.RecordDenial("p1"); !got {
		t.Fatalf("post-reset denial 2: tripped = false, want true")
	}
}

func TestResetUnknownProviderIsNoop(t *testing.T) {
	t.Parallel()
	b := New(Config{ConsecutiveThreshold: 1})
	b.Reset("never-seen") // must not panic
}

func TestConcurrentAccess(t *testing.T) {
	// Not t.Parallel(): this test is itself a concurrency stress test and
	// shouldn't be scheduled alongside unrelated parallel subtests. t is
	// still the required *testing.T signature so `go test` discovers this
	// as a test; go test -race is what actually exercises this test's
	// purpose (no assertions of its own beyond "no panic, no data race").
	t.Log("stress-testing concurrent Breaker access under go test -race")
	b := New(Config{ConsecutiveThreshold: 5, WindowSize: 10, WindowThreshold: 5})

	const goroutines = 40
	const opsPerGoroutine = 200
	providers := []string{"alpha", "beta", "gamma", "delta"}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			provider := providers[g%len(providers)]
			for i := 0; i < opsPerGoroutine; i++ {
				switch i % 4 {
				case 0:
					b.RecordDenial(provider)
				case 1:
					b.RecordCrash(provider)
				case 2:
					b.RecordSuccess(provider)
				case 3:
					if i%20 == 3 {
						b.Reset(provider)
					} else {
						b.RecordDenial(provider)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestConfigZeroValueIsNotAnError(t *testing.T) {
	t.Parallel()
	b := New(Config{})
	for i := 0; i < 10; i++ {
		if got := b.RecordDenial(fmt.Sprintf("p%d", i)); got {
			t.Fatalf("provider p%d: tripped = true, want false", i)
		}
	}
}
