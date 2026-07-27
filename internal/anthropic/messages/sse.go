package messages

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/pluggableharness/agent/pkg/sse"
)

// Scanner reads Anthropic's server-sent event stream, decoding one
// StreamEvent per call to Next.
//
// The SSE framing itself lives in pkg/sse, where every plugin author can
// reach it — nothing about reading blank-line-separated data: frames is
// Anthropic-specific. What stays here is the part that genuinely is: which
// field decides an event's type.
//
// Anthropic sends both an event: line and a JSON payload carrying its own
// "type", and this decodes from the payload alone. The JSON is
// authoritative even where it disagrees with, or the wire omits, a
// matching event: line — which is exactly why the shared scanner surfaces
// both fields and takes no position on either. Every event is decoded
// here, including ping; Handle (events.go) is where ping is filtered, so
// that choice lives in exactly one place.
type Scanner struct {
	scan *sse.Scanner
	cur  StreamEvent
	err  error
}

// NewScanner returns a Scanner reading Anthropic's SSE stream from r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{scan: sse.NewScanner(r)}
}

// Next advances the Scanner to the next decoded event. It returns false at
// EOF or once Err reports a non-nil error, and true when Event has a new
// value ready.
func (s *Scanner) Next() bool {
	if s.err != nil {
		return false
	}
	if !s.scan.Next() {
		if err := s.scan.Err(); err != nil {
			s.err = fmt.Errorf("anthropic: sse: %w", err)
		}
		return false
	}
	return s.decode(s.scan.Data())
}

// decode unmarshals one frame's payload into s.cur. A parse failure is an
// error, not a skip: a data payload that doesn't parse means something is
// wrong with the vendor's wire, not with one ignorable event.
func (s *Scanner) decode(data []byte) bool {
	// Reset before unmarshaling. Both halves of this matter and both are
	// silent corruption if skipped:
	//
	//   - encoding/json reuses a non-nil pointer field rather than
	//     allocating a fresh one, so decoding two content_block_delta
	//     events into the same struct makes both share one *StreamDelta —
	//     the second event's contents overwrite the first's, in place,
	//     after the caller already holds it.
	//   - A field absent from event N+1 keeps event N's value, so a
	//     content_block_delta would appear to carry the preceding
	//     message_start's usage.
	//
	// Zeroing costs one small struct assignment per event and removes
	// both.
	s.cur = StreamEvent{}
	if err := json.Unmarshal(data, &s.cur); err != nil {
		s.err = fmt.Errorf("anthropic: sse: decode event: %w", err)
		return false
	}
	return true
}

// Event returns the event most recently decoded by Next.
func (s *Scanner) Event() StreamEvent {
	return s.cur
}

// Err returns the error that stopped iteration, or nil at a clean EOF.
func (s *Scanner) Err() error {
	return s.err
}
