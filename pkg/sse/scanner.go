package sse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// DefaultMaxFrameBytes bounds a single SSE frame's buffer.
//
// bufio.Scanner's own 64 KiB default is unusable here because it silently
// *truncates* an over-long line rather than erroring — a corrupted frame
// that still parses is far worse than a failed read. Real vendor frames
// routinely exceed 64 KiB: an encrypted-reasoning block's base64 payload
// or a large tool-argument fragment can run into the hundreds of KB.
//
// 10 MiB is a generous ceiling that costs nothing at rest, since
// bufio.Scanner grows its buffer lazily up to the maximum, while still
// bounding worst-case memory per frame.
const DefaultMaxFrameBytes = 10 << 20

// initialFrameBytes is the buffer a Scanner starts with, grown lazily as
// needed. Sized for the common case — most frames are a few hundred bytes
// — so a stream of small frames never allocates past it.
const initialFrameBytes = 64 << 10

// doneSentinel is the data payload OpenAI-compatible vendors send to mark
// the end of a stream. Recognized by IsDone, never acted on by Scanner
// itself.
const doneSentinel = "[DONE]"

// Option configures a Scanner built by NewScanner.
type Option func(*Scanner)

// WithMaxFrameBytes overrides DefaultMaxFrameBytes. A value <= 0 is
// ignored, so a caller passing an unset config field gets the default
// rather than a Scanner that fails on the first frame.
func WithMaxFrameBytes(n int) Option {
	return func(s *Scanner) {
		if n > 0 {
			s.maxFrame = n
		}
	}
}

// Scanner reads a server-sent event stream one frame at a time.
//
// The zero value is not usable; construct one with NewScanner. A Scanner
// is not safe for concurrent use — it is driven by whichever goroutine
// owns the response body.
type Scanner struct {
	scan     *bufio.Scanner
	maxFrame int

	event string
	data  []byte
	err   error
}

// NewScanner returns a Scanner reading an SSE stream from r.
func NewScanner(r io.Reader, opts ...Option) *Scanner {
	s := &Scanner{maxFrame: DefaultMaxFrameBytes}
	for _, opt := range opts {
		opt(s)
	}
	// The initial buffer is capped at maxFrame because bufio.Scanner's own
	// limit is effectively max(maxFrame, cap(initial)) — a larger initial
	// buffer silently raises the ceiling, which would make
	// WithMaxFrameBytes a no-op for any value below initialFrameBytes.
	initial := initialFrameBytes
	if s.maxFrame < initial {
		initial = s.maxFrame
	}
	s.scan = bufio.NewScanner(r)
	s.scan.Buffer(make([]byte, 0, initial), s.maxFrame)
	return s
}

// Next advances to the next frame carrying data.
//
// It returns false at end of stream or once Err reports an error, and true
// when Data and Event have a new frame ready. A frame with no data: line
// at all (a bare comment, or an event: with no payload) is skipped rather
// than surfaced, since there is nothing for a caller to decode.
func (s *Scanner) Next() bool {
	if s.err != nil {
		return false
	}

	s.event = ""
	s.data = nil
	var payload [][]byte

	for s.scan.Scan() {
		line := s.scan.Bytes()
		switch {
		case len(line) == 0:
			// Blank line terminates a frame. A frame with no data is not
			// surfaced, but its fields are still discarded before the next.
			if len(payload) > 0 {
				s.finish(payload)
				return true
			}
			s.event = ""
		case line[0] == ':':
			// Comment, ignored per the SSE spec. Vendors use these as
			// keepalives.
		default:
			name, value, ok := splitField(line)
			if !ok {
				continue
			}
			switch string(name) {
			case "data":
				// Copied, not aliased: bufio.Scanner reuses its buffer
				// across Scan calls, so retaining the slice would let the
				// next line overwrite this frame's payload in place.
				payload = append(payload, bytes.Clone(value))
			case "event":
				s.event = string(value)
			}
			// id: and retry: are part of SSE's reconnection model, which
			// no vendor here uses for completions — ignored rather than
			// surfaced, so the API stays the two fields callers need.
		}
	}

	if err := s.scan.Err(); err != nil {
		s.err = fmt.Errorf("sse: read: %w", err)
		return false
	}
	if len(payload) > 0 {
		// The stream ended without a trailing blank line after the final
		// frame. Decode what arrived rather than silently dropping it — a
		// truncated-looking stream that still carried a complete terminal
		// event is a real case, and dropping it would turn a good stream
		// into a hang.
		s.finish(payload)
		return true
	}
	return false
}

// finish joins payload per SSE's multi-line-data rule and records it as
// the current frame.
func (s *Scanner) finish(payload [][]byte) {
	s.data = bytes.Join(payload, []byte("\n"))
}

// splitField splits an SSE line into its field name and value, applying
// the spec's rule that a single space after the colon is part of the
// delimiter rather than the value. A line with no colon is not a field.
func splitField(line []byte) (name, value []byte, ok bool) {
	name, value, ok = bytes.Cut(line, []byte(":"))
	if !ok {
		return nil, nil, false
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return name, value, true
}

// Data returns the current frame's payload, with multi-line data already
// joined by "\n".
//
// The returned slice is owned by the caller and is not reused by a later
// Next, so it is safe to retain or unmarshal from directly.
func (s *Scanner) Data() []byte {
	return s.data
}

// IsDone reports whether the current frame is the "[DONE]" sentinel
// OpenAI-compatible vendors send to mark the end of a stream.
//
// Scanner never acts on it: a sentinel is still surfaced as an ordinary
// frame, and a caller that does not use this vendor convention can ignore
// this method entirely. Anthropic, for one, has no such sentinel and ends
// its stream with a real terminal event instead.
func (s *Scanner) IsDone() bool {
	return string(s.data) == doneSentinel
}

// Event returns the current frame's event: field, or "" when the frame
// carried none.
//
// Whether this matters is vendor-specific and deliberately left to the
// caller: some vendors dispatch on it, others treat the data payload's own
// type field as authoritative and ignore this entirely.
func (s *Scanner) Event() string {
	return s.event
}

// Err returns the error that stopped iteration, or nil at a clean end of
// stream.
func (s *Scanner) Err() error {
	return s.err
}
