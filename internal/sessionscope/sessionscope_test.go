package sessionscope

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

func TestRegistry_grantThenRelease(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	key := Key{Category: commonv1.Category_CATEGORY_TOOL, Name: "ripgrep"}

	if r.Authorized(key, "sess-1") {
		t.Fatal("Authorized before any Grant: got true, want false")
	}

	release := r.Grant(key, "sess-1")
	if !r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after Grant: got false, want true")
	}

	release()
	if r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after release: got true, want false")
	}
}

func TestRegistry_nestedGrants(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	key := Key{Category: commonv1.Category_CATEGORY_MODEL, Name: "anthropic"}

	release1 := r.Grant(key, "sess-1")
	release2 := r.Grant(key, "sess-1")

	if !r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after two grants: got false, want true")
	}

	release1()
	if !r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after releasing one of two grants: got false, want true")
	}

	release2()
	if r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after releasing both grants: got true, want false")
	}
}

func TestRegistry_idempotentRelease(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	key := Key{Category: commonv1.Category_CATEGORY_CONTEXT, Name: "docsource"}

	release := r.Grant(key, "sess-1")

	release()
	release()
	release()

	if r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after idempotent releases: got true, want false")
	}

	// A sibling grant for the same pair must be unaffected by the
	// already-released (and repeatedly-called) release above.
	release2 := r.Grant(key, "sess-1")
	if !r.Authorized(key, "sess-1") {
		t.Fatal("Authorized after fresh Grant following idempotent releases: got false, want true")
	}
	release2()
}

func TestRegistry_independentSessions(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	key := Key{Category: commonv1.Category_CATEGORY_MEMORY, Name: "sqlite"}

	releaseA := r.Grant(key, "sess-A")

	if r.Authorized(key, "sess-B") {
		t.Fatal("Authorized for ungranted session sess-B: got true, want false")
	}
	if !r.Authorized(key, "sess-A") {
		t.Fatal("Authorized for granted session sess-A: got false, want true")
	}

	releaseB := r.Grant(key, "sess-B")
	releaseA()

	if r.Authorized(key, "sess-A") {
		t.Fatal("Authorized for released session sess-A: got true, want false")
	}
	if !r.Authorized(key, "sess-B") {
		t.Fatal("Authorized for still-granted session sess-B: got false, want true")
	}

	releaseB()
}

func TestRegistry_independentKeys(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	sessionID := "sess-shared"
	keyA := Key{Category: commonv1.Category_CATEGORY_TOOL, Name: "ripgrep"}
	keyB := Key{Category: commonv1.Category_CATEGORY_TOOL, Name: "fd"}
	keyC := Key{Category: commonv1.Category_CATEGORY_MODEL, Name: "ripgrep"} // same name, different category

	releaseA := r.Grant(keyA, sessionID)

	if r.Authorized(keyB, sessionID) {
		t.Fatal("Authorized for ungranted key (different name): got true, want false")
	}
	if r.Authorized(keyC, sessionID) {
		t.Fatal("Authorized for ungranted key (different category): got true, want false")
	}
	if !r.Authorized(keyA, sessionID) {
		t.Fatal("Authorized for granted key: got false, want true")
	}

	releaseA()
	if r.Authorized(keyA, sessionID) {
		t.Fatal("Authorized after release: got true, want false")
	}
}

func TestRegistry_sessions(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	key := Key{Category: commonv1.Category_CATEGORY_FRONTEND, Name: "tui"}

	if got := r.Sessions(key); len(got) != 0 {
		t.Fatalf("Sessions before any Grant: got %v, want empty", got)
	}

	releaseC := r.Grant(key, "sess-c")
	releaseA := r.Grant(key, "sess-a")
	releaseB := r.Grant(key, "sess-b")
	// A second grant on an already-granted session must not add a
	// duplicate entry to Sessions.
	releaseB2 := r.Grant(key, "sess-b")

	want := []string{"sess-a", "sess-b", "sess-c"}
	if got := r.Sessions(key); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sessions after grants: got %v, want %v", got, want)
	}

	releaseA()
	releaseB()
	releaseB2()

	want = []string{"sess-c"}
	if got := r.Sessions(key); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sessions after partial release: got %v, want %v", got, want)
	}

	releaseC()
	if got := r.Sessions(key); len(got) != 0 {
		t.Fatalf("Sessions after full release: got %v, want empty", got)
	}
}

func TestRegistry_internalMapCleanup(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	key := Key{Category: commonv1.Category_CATEGORY_WIDGET, Name: "chart"}

	release := r.Grant(key, "sess-1")
	release()

	r.mu.RLock()
	_, keyStillPresent := r.grants[key]
	r.mu.RUnlock()

	if keyStillPresent {
		t.Fatal("outer map key still present after full release: expected cleanup")
	}
}

func TestKeyFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  *commonv1.ProducerRef
		want Key
	}{
		{
			name: "tool producer",
			ref: &commonv1.ProducerRef{
				Category: commonv1.Category_CATEGORY_TOOL,
				Name:     "ripgrep",
				Version:  "1.2.3",
			},
			want: Key{Category: commonv1.Category_CATEGORY_TOOL, Name: "ripgrep"},
		},
		{
			name: "model producer, version ignored",
			ref: &commonv1.ProducerRef{
				Category: commonv1.Category_CATEGORY_MODEL,
				Name:     "anthropic",
				Version:  "9.9.9",
			},
			want: Key{Category: commonv1.Category_CATEGORY_MODEL, Name: "anthropic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := KeyFor(tt.ref); got != tt.want {
				t.Fatalf("KeyFor(%+v) = %+v, want %+v", tt.ref, got, tt.want)
			}
		})
	}
}

// TestRegistry_concurrencyStress hammers a single shared Registry from
// many goroutines performing Grant, Authorized, Sessions, and release in
// an interleaved, unpredictable order. It intentionally does not use
// t.Parallel(): this test's own goroutines are what exercise concurrency,
// and running the test itself in parallel with unrelated tests wouldn't
// add coverage while making failures harder to reproduce. The correctness
// assertion here is limited to "no panic, no data race" (run under
// -race) during the chaotic phase; a final deterministic phase releases
// every grant this test itself took and then asserts Authorized is false
// for all of them.
func TestRegistry_concurrencyStress(t *testing.T) {
	r := NewRegistry()

	const (
		numKeys     = 4
		numSessions = 4
		numWorkers  = 50
		numRounds   = 200
	)

	keys := make([]Key, numKeys)
	for i := range keys {
		keys[i] = Key{Category: commonv1.Category(i%7 + 1), Name: fmt.Sprintf("plugin-%d", i)}
	}
	sessionIDs := make([]string, numSessions)
	for i := range sessionIDs {
		sessionIDs[i] = fmt.Sprintf("sess-%d", i)
	}

	var wg sync.WaitGroup
	var releasesMu sync.Mutex
	var releases []func()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < numRounds; round++ {
				key := keys[(worker+round)%numKeys]
				sessionID := sessionIDs[(worker*round+round)%numSessions]

				release := r.Grant(key, sessionID)

				// Exercise the read paths concurrently with other
				// goroutines' Grant/release calls.
				_ = r.Authorized(key, sessionID)
				_ = r.Sessions(key)

				releasesMu.Lock()
				releases = append(releases, release)
				releasesMu.Unlock()

				// Release roughly half of what we grant immediately,
				// to mix outstanding and settled grants throughout the
				// stress run; the rest are released in the cleanup
				// phase below.
				if round%2 == 0 {
					release()
				}
			}
		}(w)
	}

	wg.Wait()

	// Deterministic cleanup phase: release everything still outstanding
	// and assert the Registry ends up fully unauthorized for every
	// (key, session) pair this test touched.
	for _, release := range releases {
		release()
		release() // idempotency, exercised again under the stress data set
	}

	for _, key := range keys {
		for _, sessionID := range sessionIDs {
			if r.Authorized(key, sessionID) {
				t.Fatalf("Authorized(%+v, %q) = true after full cleanup, want false", key, sessionID)
			}
		}
		if got := r.Sessions(key); len(got) != 0 {
			t.Fatalf("Sessions(%+v) = %v after full cleanup, want empty", key, got)
		}
	}
}
