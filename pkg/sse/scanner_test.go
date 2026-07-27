package sse_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/pluggableharness/agent/pkg/sse"
)

// frames drains a Scanner into (event, data) pairs, failing on a read
// error so a test asserting frames never also has to assert Err.
func frames(t *testing.T, s *sse.Scanner) [][2]string {
	t.Helper()

	var got [][2]string
	for s.Next() {
		got = append(got, [2]string{s.Event(), string(s.Data())})
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	return got
}

func TestScanner_framesAndFields(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want [][2]string
	}{
		"one frame": {
			in:   "data: {\"a\":1}\n\n",
			want: [][2]string{{"", `{"a":1}`}},
		},
		"event field is surfaced": {
			in:   "event: delta\ndata: {\"a\":1}\n\n",
			want: [][2]string{{"delta", `{"a":1}`}},
		},
		"comments are ignored": {
			in:   ": keepalive\ndata: x\n\n",
			want: [][2]string{{"", "x"}},
		},
		"multi-line data joins with newline": {
			in:   "data: one\ndata: two\n\n",
			want: [][2]string{{"", "one\ntwo"}},
		},
		"only one leading space is stripped": {
			in:   "data:  padded\n\n",
			want: [][2]string{{"", " padded"}},
		},
		"no space after colon still parses": {
			in:   "data:tight\n\n",
			want: [][2]string{{"", "tight"}},
		},
		"unterminated final frame is still yielded": {
			in:   "data: a\n\ndata: b",
			want: [][2]string{{"", "a"}, {"", "b"}},
		},
		"frames with no data are skipped": {
			in:   "event: ping\n\ndata: real\n\n",
			want: [][2]string{{"", "real"}},
		},
		"id and retry are ignored": {
			in:   "id: 7\nretry: 100\ndata: x\n\n",
			want: [][2]string{{"", "x"}},
		},
		"empty stream yields nothing": {
			in:   "",
			want: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := frames(t, sse.NewScanner(strings.NewReader(tt.in)))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d frames %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("frame %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestScanner_eventFieldDoesNotLeakAcrossFrames(t *testing.T) {
	t.Parallel()

	// A frame carrying only an event: name must not have that name stick
	// to the next frame — that would make a caller dispatching on Event()
	// handle a payload as the wrong kind.
	got := frames(t, sse.NewScanner(strings.NewReader("event: ping\n\ndata: x\n\n")))

	if len(got) != 1 {
		t.Fatalf("got %v, want one frame", got)
	}
	if got[0][0] != "" {
		t.Errorf("Event() = %q, want empty — the preceding frame's name leaked", got[0][0])
	}
}

func TestScanner_dataIsNotAliasedAcrossFrames(t *testing.T) {
	t.Parallel()

	// bufio.Scanner reuses its buffer, so a Scanner retaining the raw slice
	// would let a later line overwrite an earlier frame's payload in place
	// — silent corruption a caller holding the slice cannot detect.
	s := sse.NewScanner(strings.NewReader("data: first\n\ndata: second\n\n"))

	if !s.Next() {
		t.Fatal("Next() = false, want the first frame")
	}
	held := s.Data()

	if !s.Next() {
		t.Fatal("Next() = false, want the second frame")
	}
	if string(held) != "first" {
		t.Errorf("the first frame's data became %q after advancing, want %q", held, "first")
	}
}

func TestScanner_isDone(t *testing.T) {
	t.Parallel()

	// The sentinel is surfaced as an ordinary frame, not swallowed: a
	// vendor that does not use this convention must see every frame, and
	// one that does decides for itself when to stop.
	s := sse.NewScanner(strings.NewReader("data: {\"a\":1}\n\ndata: [DONE]\n\n"))

	if !s.Next() || s.IsDone() {
		t.Fatalf("first frame: IsDone() = true, want false")
	}
	if !s.Next() {
		t.Fatal("Next() = false, want the sentinel frame")
	}
	if !s.IsDone() {
		t.Errorf("IsDone() = false for %q, want true", s.Data())
	}
}

func TestScanner_overlongFrameErrorsRatherThanTruncating(t *testing.T) {
	t.Parallel()

	// The whole reason DefaultMaxFrameBytes exists: bufio.Scanner's own
	// default silently truncates, and a corrupted frame that still parses
	// is far worse than a failed read.
	s := sse.NewScanner(strings.NewReader("data: "+strings.Repeat("x", 200)+"\n\n"), sse.WithMaxFrameBytes(64))

	if s.Next() {
		t.Fatalf("Next() = true with data %q, want false on an over-long frame", s.Data())
	}
	if err := s.Err(); err == nil {
		t.Fatal("Err() = nil, want an error rather than a silently truncated frame")
	}
}

func TestScanner_maxFrameBytesIgnoresNonPositive(t *testing.T) {
	t.Parallel()

	// A caller passing an unset config field gets the default rather than
	// a Scanner that fails on the first frame.
	got := frames(t, sse.NewScanner(strings.NewReader("data: x\n\n"), sse.WithMaxFrameBytes(0)))
	if len(got) != 1 {
		t.Fatalf("got %v, want one frame", got)
	}
}

// errReader fails after yielding its payload, so the scanner's read-error
// path is exercised without a real network.
type errReader struct {
	payload string
	read    bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.read {
		return 0, errors.New("boom")
	}
	e.read = true
	return copy(p, e.payload), nil
}

func TestScanner_readErrorSurfacesAndStops(t *testing.T) {
	t.Parallel()

	s := sse.NewScanner(&errReader{payload: "data: a\n\n"})

	if !s.Next() {
		t.Fatal("Next() = false, want the frame that arrived before the error")
	}
	if s.Next() {
		t.Error("Next() = true after a read error, want false")
	}
	if err := s.Err(); err == nil {
		t.Fatal("Err() = nil, want the read error")
	}
	// Err is sticky: a caller looping on Next must not be able to resume
	// past a failed read.
	if s.Next() {
		t.Error("Next() = true on a second call after an error, want false")
	}
}

var _ io.Reader = (*errReader)(nil)
