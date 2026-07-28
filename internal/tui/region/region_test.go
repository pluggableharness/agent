package region_test

import (
	"testing"

	"github.com/pluggableharness/agent/internal/tui/region"
	"github.com/pluggableharness/agent/pkg/render"
)

func place(s *region.Store, r region.Region, text string, replace bool, priority *int32, p region.Producer, seq uint64) {
	s.PlaceContent(r, render.Tree(render.Text(text)), p, seq, replace, priority)
}

func texts(t *testing.T, in []region.Placement) []string {
	t.Helper()

	out := make([]string, 0, len(in))
	for _, p := range in {
		out = append(out, p.Tree.GetRoot().GetText().GetContent())
	}

	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   region.Region
		want region.Region
	}{
		{"unspecified folds to main chat", region.Unspecified, region.MainChat},
		{"known region is preserved", region.Sidebar, region.Sidebar},
		{"future region folds to main chat", region.Region(42), region.MainChat},
		{"negative region folds to main chat", region.Region(-1), region.MainChat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := region.Normalize(tc.in); got != tc.want {
				t.Fatalf("Normalize(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPlaceAppendsByDefault(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	p := region.Producer{Category: "tool", Name: "fs"}

	place(s, region.MainChat, "first", false, nil, p, 1)
	place(s, region.MainChat, "second", false, nil, p, 2)

	got := texts(t, s.Contents(region.MainChat))
	if want := []string{"first", "second"}; !equal(got, want) {
		t.Fatalf("append semantics broken: got %v, want %v", got, want)
	}
}

// Replace is scoped to the producer that sent it. The protocol's default is
// coexistence, not exclusivity, so one producer replacing its own content must
// never evict another's from the same region.
func TestReplaceIsScopedToOneProducer(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	git := region.Producer{Category: "widget", Name: "git"}
	ctx := region.Producer{Category: "widget", Name: "context"}

	place(s, region.Sidebar, "git v1", true, nil, git, 1)
	place(s, region.Sidebar, "context", true, nil, ctx, 2)
	place(s, region.Sidebar, "git v2", true, nil, git, 3)

	got := texts(t, s.Contents(region.Sidebar))
	if want := []string{"context", "git v2"}; !equal(got, want) {
		t.Fatalf("replace evicted the wrong producer: got %v, want %v", got, want)
	}
}

// Unset priority sorts after every ranked placement, and sequence is the only
// tiebreak. Wall clock is never consulted.
func TestContentsOrdering(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	p := region.Producer{Category: "widget", Name: "w"}

	lo, hi := int32(1), int32(50)

	place(s, region.Sidebar, "unranked-early", false, nil, p, 1)
	place(s, region.Sidebar, "ranked-high", false, &hi, p, 2)
	place(s, region.Sidebar, "unranked-late", false, nil, p, 3)
	place(s, region.Sidebar, "ranked-low", false, &lo, p, 4)

	got := texts(t, s.Contents(region.Sidebar))
	want := []string{"ranked-low", "ranked-high", "unranked-early", "unranked-late"}

	if !equal(got, want) {
		t.Fatalf("ordering wrong\ngot:  %v\nwant: %v", got, want)
	}
}

// A zero priority is a ranked placement, not an unset one — the distinction the
// optional field exists to carry.
func TestZeroPriorityIsRanked(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	p := region.Producer{Category: "widget", Name: "w"}
	zero := int32(0)

	place(s, region.Sidebar, "unranked", false, nil, p, 1)
	place(s, region.Sidebar, "ranked-zero", false, &zero, p, 2)

	got := texts(t, s.Contents(region.Sidebar))
	if want := []string{"ranked-zero", "unranked"}; !equal(got, want) {
		t.Fatalf("zero priority treated as unset: got %v, want %v", got, want)
	}
}

func TestContentsIsDeterministic(t *testing.T) {
	t.Parallel()

	s := region.NewStore()

	for i := range 20 {
		p := region.Producer{Category: "widget", Name: string(rune('a' + i%5))}
		place(s, region.MainChat, "n", false, nil, p, uint64(i))
	}

	first := texts(t, s.Contents(region.MainChat))

	for range 25 {
		if got := texts(t, s.Contents(region.MainChat)); !equal(got, first) {
			t.Fatal("Contents returned a different order across calls; paint order is not deterministic")
		}
	}
}

func TestContentsReturnsACopy(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	p := region.Producer{Category: "tool", Name: "fs"}

	place(s, region.MainChat, "a", false, nil, p, 1)
	place(s, region.MainChat, "b", false, nil, p, 2)

	got := s.Contents(region.MainChat)
	got[0], got[1] = got[1], got[0]

	after := texts(t, s.Contents(region.MainChat))
	if want := []string{"a", "b"}; !equal(after, want) {
		t.Fatalf("mutating the returned slice disturbed the store: got %v", after)
	}
}

func TestPlaceIgnoresNilContent(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	p := region.Producer{Category: "tool", Name: "fs"}

	s.PlaceContent(region.MainChat, nil, p, 1, false, nil)
	s.PlaceContent(region.MainChat, nil, p, 2, false, nil)

	if got := s.Contents(region.MainChat); len(got) != 0 {
		t.Fatalf("nil content was stored: %d placements", len(got))
	}
}

func TestContentsOfUnknownRegionIsEmpty(t *testing.T) {
	t.Parallel()

	if got := region.NewStore().Contents(region.Region(99)); got != nil {
		t.Fatalf("expected nil for out-of-range region, got %v", got)
	}
}

func TestStreamsAccumulateByTargetID(t *testing.T) {
	t.Parallel()

	s := region.NewStore()

	s.Delta("a", "hello ")
	s.Delta("b", "other")
	s.Delta("a", "world")

	got := s.Streams()
	if len(got) != 2 {
		t.Fatalf("got %d streams, want 2", len(got))
	}

	// Arrival order, not map order.
	if got[0].TargetID != "a" || got[0].Text != "hello world" {
		t.Fatalf("stream a = %+v, want accumulated text in arrival position 0", got[0])
	}

	if got[1].TargetID != "b" || got[1].Text != "other" {
		t.Fatalf("stream b = %+v", got[1])
	}
}

func TestClearStream(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	s.Delta("a", "x")
	s.Delta("b", "y")
	s.ClearStream("a")

	got := s.Streams()
	if len(got) != 1 || got[0].TargetID != "b" {
		t.Fatalf("ClearStream removed the wrong buffer: %+v", got)
	}

	s.ClearProducerStreams()

	if len(s.Streams()) != 0 {
		t.Fatal("ClearProducerStreams left buffers behind")
	}
}

func TestReset(t *testing.T) {
	t.Parallel()

	s := region.NewStore()
	p := region.Producer{Category: "tool", Name: "fs"}

	place(s, region.MainChat, "a", false, nil, p, 1)
	place(s, region.Sidebar, "b", false, nil, p, 2)
	s.Delta("t", "streaming")

	s.Reset()

	if len(s.Contents(region.MainChat)) != 0 ||
		len(s.Contents(region.Sidebar)) != 0 ||
		len(s.Streams()) != 0 {
		t.Fatal("Reset left content behind; a re-backfill would stack on stale state")
	}
}
