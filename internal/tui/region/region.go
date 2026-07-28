package region

import (
	"math"
	"sort"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// Region is a local layout pane for this terminal shell — not a wire type.
// The protocol retired placement regions; transcript content is always
// main chat, and other chrome (status, sidebar chrome, input) is owned by
// the frontend from SessionState and MetadataBlock surfaces. This enum
// remains only so the shell's internal layout splitter keeps a stable
// paint order for local UI chrome and transcript trees.
type Region int

const (
	// Unspecified means "producer did not choose"; Normalize maps it to MainChat.
	Unspecified Region = iota
	// MainChat is the conversation transcript.
	MainChat
	// Sidebar is a persistent side panel (local chrome / metadata).
	Sidebar
	// TopBar is a persistent header bar (SessionState).
	TopBar
	// InputBar is the area around the operator's input box.
	InputBar
	// HotkeyHints is contextual hotkey/command hints.
	HotkeyHints
	// Overlay is a modal or floating layer.
	Overlay
)

// count is the number of Region values, including Unspecified at zero.
// The store indexes a fixed-length array by enum value so paint order
// never depends on map iteration.
const count = 7

// Producer identifies the plugin that contributed a placement. Identity is
// always server-derived from the authenticated connection.
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
// StreamDeltas carry. Streams paint at the tail of MainChat, after every
// settled placement, because they are by definition the live edge of the
// transcript.
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
// session and mutates it only from the Bubble Tea update goroutine.
type Store struct {
	regions [count][]Placement
	streams []Stream
	arrival uint64
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{} }

// inRange reports whether r is a region value this build knows how to index.
func inRange(r Region) bool {
	return r >= 0 && int(r) < count
}

// Normalize maps a region value onto the region this shell will actually
// use for it. Unspecified and unrecognized values fold to MainChat so
// content is never silently dropped.
func Normalize(r Region) Region {
	if !inRange(r) || r == Unspecified {
		return MainChat
	}
	return r
}

// PlaceContent adds a RenderTree to the store under region r.
func (s *Store) PlaceContent(r Region, tree *renderv1.RenderTree, p Producer, sequence uint64, replace bool, priority *int32) {
	if tree == nil {
		return
	}
	r = Normalize(r)
	next := Placement{
		Producer: p,
		Sequence: sequence,
		Tree:     tree,
	}
	if priority != nil {
		next.Priority = *priority
		next.Ranked = true
	}
	if replace {
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
// slice is a fresh copy.
func (s *Store) Contents(r Region) []Placement {
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

// rank projects a placement's priority onto a total order.
func rank(p Placement) int64 {
	if !p.Ranked {
		return math.MaxInt64
	}
	return int64(p.Priority)
}

// Delta appends streamed text to the buffer for targetID, creating it on first
// sight.
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

// ClearStream drops the buffer for targetID.
func (s *Store) ClearStream(targetID string) {
	out := s.streams[:0]
	for _, st := range s.streams {
		if st.TargetID != targetID {
			out = append(out, st)
		}
	}
	s.streams = out
}

// ClearProducerStreams drops every live buffer.
func (s *Store) ClearProducerStreams() { s.streams = nil }

// Streams returns the live streamed blocks in arrival order.
func (s *Store) Streams() []Stream {
	out := make([]Stream, len(s.streams))
	copy(out, s.streams)
	sort.SliceStable(out, func(i, j int) bool { return out[i].first < out[j].first })
	return out
}

// Reset empties the store.
func (s *Store) Reset() {
	for i := range s.regions {
		s.regions[i] = nil
	}
	s.streams = nil
	s.arrival = 0
}
