package region

import (
	"math"
	"sort"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// count is the number of values in the Region enum, including
// REGION_UNSPECIFIED at zero. The store indexes a fixed-length array by enum
// value so paint order never depends on map iteration.
const count = 7

// Producer identifies the plugin that contributed a placement. Identity is
// always server-derived from the authenticated connection — a plugin cannot
// declare an identity other than its own — so the shell treats this as
// trustworthy for the purpose of scoping replace semantics.
type Producer struct {
	Category string
	Name     string
}

// Placement is one producer's contribution to one region.
type Placement struct {
	// Producer is who contributed this content.
	Producer Producer
	// Priority is the producer's ordering hint. Ranked reports whether it was
	// set at all: an unset priority is not zero, it sorts after every ranked
	// placement.
	Priority int32
	Ranked   bool
	// Sequence is the kernel's event sequence, the sole ordering tiebreak.
	Sequence uint64
	// Tree is the content to paint.
	Tree *renderv1.RenderTree
}

// Stream is an in-progress streamed text block, correlated by the target ID
// the kernel's stream_delta events carry. Streams paint at the tail of
// REGION_MAIN_CHAT, after every settled placement, because they are by
// definition the live edge of the transcript.
type Stream struct {
	TargetID string
	Text     string
	// first is the arrival index, used to keep multiple concurrent streams in
	// a stable order without consulting the clock.
	first uint64
}

// Store holds all placed content for a single session.
//
// A Store is not safe for concurrent use. The shell owns one per attached
// session and mutates it only from the Bubble Tea update goroutine, which is
// what makes the absence of locking correct rather than merely convenient.
type Store struct {
	regions [count][]Placement
	streams []Stream
	arrival uint64
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{} }

// inRange reports whether r is a region value this build knows how to index.
// A value outside the range is one added to the enum after this shell shipped;
// callers fold it into REGION_MAIN_CHAT rather than dropping the content.
func inRange(r renderv1.Region) bool {
	return r >= 0 && int(r) < count
}

// Normalize maps a wire region value onto the region this shell will actually
// use for it. REGION_UNSPECIFIED means "the producer did not choose", which the
// protocol defines as REGION_MAIN_CHAT; an unrecognized value from a newer
// protocol build folds to the same place, since the alternative is silently
// dropping content the frontend is required to render gracefully.
func Normalize(r renderv1.Region) renderv1.Region {
	if !inRange(r) || r == renderv1.Region_REGION_UNSPECIFIED {
		return renderv1.Region_REGION_MAIN_CHAT
	}

	return r
}

// Place adds content to the store. When pc.Replace is set, the placement
// supersedes that producer's prior placements in the same region and leaves
// every other producer's untouched; otherwise it is appended alongside them.
//
// Place is a no-op for nil content, so a malformed event degrades to "nothing
// shown" rather than a panic in the paint path.
func (s *Store) Place(pc *renderv1.PlacedContent, p Producer, sequence uint64) {
	if pc == nil || pc.GetContent() == nil {
		return
	}

	r := Normalize(pc.GetRegion())
	next := Placement{
		Producer: p,
		Sequence: sequence,
		Tree:     pc.GetContent(),
	}

	if pc.Priority != nil {
		next.Priority = pc.GetPriority()
		next.Ranked = true
	}

	if pc.GetReplace() {
		s.regions[r] = deleteProducer(s.regions[r], p)
	}

	s.regions[r] = append(s.regions[r], next)
}

// deleteProducer removes every placement contributed by p, preserving the
// relative order of the rest.
func deleteProducer(in []Placement, p Producer) []Placement {
	out := in[:0]

	for _, pl := range in {
		if pl.Producer != p {
			out = append(out, pl)
		}
	}

	return out
}

// Contents returns the placements for a region in paint order. The returned
// slice is a fresh copy, so a caller may hold or reorder it without disturbing
// the store.
func (s *Store) Contents(r renderv1.Region) []Placement {
	if !inRange(r) {
		return nil
	}

	out := make([]Placement, len(s.regions[r]))
	copy(out, s.regions[r])

	sort.SliceStable(out, func(i, j int) bool {
		li, lj := rank(out[i]), rank(out[j])
		if li != lj {
			return li < lj
		}

		return out[i].Sequence < out[j].Sequence
	})

	return out
}

// rank projects a placement's priority onto a total order. An unset priority
// sorts after every ranked placement, which is what "unset = declaration
// order" means once ranked entries are allowed to jump the queue.
func rank(p Placement) int64 {
	if !p.Ranked {
		return math.MaxInt64
	}

	return int64(p.Priority)
}

// Delta appends streamed text to the buffer for targetID, creating it on first
// sight. Consecutive deltas for one target accumulate into a single growing
// block rather than becoming separate lines.
func (s *Store) Delta(targetID, text string) {
	for i := range s.streams {
		if s.streams[i].TargetID == targetID {
			s.streams[i].Text += text

			return
		}
	}

	s.arrival++
	s.streams = append(s.streams, Stream{TargetID: targetID, Text: text, first: s.arrival})
}

// ClearStream drops the buffer for targetID. The shell calls this when the
// finished render for a streamed block arrives, so the completed content
// replaces the live buffer instead of appearing twice.
func (s *Store) ClearStream(targetID string) {
	out := s.streams[:0]

	for _, st := range s.streams {
		if st.TargetID != targetID {
			out = append(out, st)
		}
	}

	s.streams = out
}

// ClearProducerStreams drops every live buffer, which is the coarse form of
// ClearStream used when a producer settles content and the shell cannot
// correlate it to a specific target ID.
func (s *Store) ClearProducerStreams() { s.streams = nil }

// Streams returns the live streamed blocks in arrival order.
func (s *Store) Streams() []Stream {
	out := make([]Stream, len(s.streams))
	copy(out, s.streams)

	sort.SliceStable(out, func(i, j int) bool { return out[i].first < out[j].first })

	return out
}

// Reset empties the store, used when a session is detached or re-backfilled so
// replayed history does not stack on top of what was already painted.
func (s *Store) Reset() {
	for i := range s.regions {
		s.regions[i] = nil
	}

	s.streams = nil
	s.arrival = 0
}
