package messages

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// maxSSELineBytes bounds a single SSE line's buffer. bufio.Scanner's
// default 64 KiB cap silently truncates a line rather than erroring, and
// Anthropic can emit a single data: line well past that — a
// redacted_thinking block's base64 payload or a large input_json_delta
// fragment can run into the hundreds of KB. 10 MiB is a generous ceiling
// that costs nothing at rest (bufio.Scanner grows its buffer lazily up to
// this max) while still bounding worst-case memory per line.
const maxSSELineBytes = 10 << 20

// Scanner reads Anthropic's server-sent event stream, decoding one
// StreamEvent per call to Next.
//
// Anthropic's wire format is one "name: value" field per line, blank-line
// separated events, and ":"-prefixed comment lines to ignore. Scanner
// decodes every event it sees — including ping — from the data: line's own
// JSON payload rather than the event: line, per this package's CLAUDE.md:
// the JSON's "type" field is authoritative even where it disagrees with,
// or the wire omits, a matching event: line. Handle (events.go) is where
// ping is filtered out; Scanner itself surfaces it so that choice lives in
// exactly one place.
type Scanner struct {
	scan *bufio.Scanner
	cur  StreamEvent
	err  error
}

// NewScanner returns a Scanner reading Anthropic's SSE stream from r.
func NewScanner(r io.Reader) *Scanner {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)
	return &Scanner{scan: scan}
}

// Next advances the Scanner to the next decoded event. It returns false at
// EOF or once Err reports a non-nil error, and true when Event has a new
// value ready.
func (s *Scanner) Next() bool {
	if s.err != nil {
		return false
	}

	var dataLines []string
	for s.scan.Scan() {
		line := s.scan.Text()
		switch {
		case line == "":
			if len(dataLines) > 0 {
				return s.decode(dataLines)
			}
		case strings.HasPrefix(line, ":"):
			// Comment line, ignored per the SSE spec.
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		// Any other field (event:, id:, retry:) is ignored — see the
		// package-level comment on why decoding relies on data: alone.
	}
	if err := s.scan.Err(); err != nil {
		s.err = fmt.Errorf("anthropic: sse: read: %w", err)
		return false
	}
	if len(dataLines) > 0 {
		// The stream ended without a trailing blank line after the final
		// event's data — decode what arrived rather than silently dropping it.
		return s.decode(dataLines)
	}
	return false
}

// decode joins dataLines per the SSE multi-line-data rule (concatenated
// with "\n", even though Anthropic only ever sends one data: line per
// event) and unmarshals the result into s.cur. A parse failure is an
// error, not a skip: a data: line that doesn't parse means something is
// wrong with the vendor's wire, not with one ignorable event.
func (s *Scanner) decode(dataLines []string) bool {
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
	if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &s.cur); err != nil {
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
