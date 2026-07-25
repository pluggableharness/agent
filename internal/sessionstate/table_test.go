package sessionstate

import (
	"sync"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/bounds"
)

func TestTable_PutGetRemove(t *testing.T) {
	t.Parallel()
	table := NewTable()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	if _, ok := table.Get("sess-1"); ok {
		t.Fatal("Get on empty Table returned ok=true")
	}

	table.Put("sess-1", live)
	got, ok := table.Get("sess-1")
	if !ok {
		t.Fatal("Get after Put: ok=false")
	}
	if got != live {
		t.Errorf("Get returned %p, want %p", got, live)
	}

	table.Remove("sess-1")
	if _, ok := table.Get("sess-1"); ok {
		t.Fatal("Get after Remove returned ok=true")
	}
}

func TestTable_Put_replacesExisting(t *testing.T) {
	t.Parallel()
	table := NewTable()
	first, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})
	second, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	table.Put("sess-1", first)
	table.Put("sess-1", second)

	got, ok := table.Get("sess-1")
	if !ok {
		t.Fatal("Get: ok=false")
	}
	if got != second {
		t.Errorf("Get returned %p, want the replacement %p", got, second)
	}
}

func TestTable_concurrentAccess(t *testing.T) {
	t.Parallel()
	table := NewTable()
	live, _ := newTestLive(t, bounds.Limits{}, nil, time.Time{})

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n * 3)
	for i := range n {
		id := "sess"
		go func() { defer wg.Done(); table.Put(id, live) }()
		go func() { defer wg.Done(); table.Get(id) }()
		go func() { defer wg.Done(); table.Remove(id) }()
		_ = i
	}
	wg.Wait()
}
