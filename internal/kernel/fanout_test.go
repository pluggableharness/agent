package kernel

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

// recordingHandler is a slog.Handler that records what it was asked to
// handle, so a test can assert on fanout without parsing formatted output.
type recordingHandler struct {
	minLevel slog.Level
	err      error

	records *[]slog.Record
	attrs   *[]slog.Attr
	groups  *[]string
}

func newRecordingHandler(minLevel slog.Level, err error) *recordingHandler {
	return &recordingHandler{
		minLevel: minLevel,
		err:      err,
		records:  &[]slog.Record{},
		attrs:    &[]slog.Attr{},
		groups:   &[]string{},
	}
}

func (h *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return h.err
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	*h.attrs = append(*h.attrs, attrs...)
	return h
}

func (h *recordingHandler) WithGroup(name string) slog.Handler {
	*h.groups = append(*h.groups, name)
	return h
}

func TestNewFanoutHandler_dropsNilTargets(t *testing.T) {
	t.Parallel()

	only := newRecordingHandler(slog.LevelDebug, nil)
	// A single live target is returned unwrapped: the telemetry-off case
	// must not pay for an indirection it has no use for.
	if got := newFanoutHandler(nil, only, nil); got != slog.Handler(only) {
		t.Errorf("newFanoutHandler with one live target = %T, want the target itself", got)
	}
}

func TestNewFanoutHandler_noTargetsIsUsable(t *testing.T) {
	t.Parallel()

	h := newFanoutHandler(nil, nil)
	if h == nil {
		t.Fatal("newFanoutHandler with no targets = nil, want a discarding handler")
	}
	if h.Enabled(context.Background(), slog.LevelError) {
		t.Error("an empty fanout reported Enabled")
	}
	if err := h.Handle(context.Background(), slog.Record{}); err != nil {
		t.Errorf("Handle on an empty fanout = %v, want nil", err)
	}
}

func TestFanoutHandler_writesToEveryEnabledTarget(t *testing.T) {
	t.Parallel()

	loud := newRecordingHandler(slog.LevelDebug, nil)
	quiet := newRecordingHandler(slog.LevelError, nil)
	h := newFanoutHandler(loud, quiet)

	slog.New(h).Info("hello")

	if len(*loud.records) != 1 {
		t.Errorf("debug-level target got %d records, want 1", len(*loud.records))
	}
	if len(*quiet.records) != 0 {
		t.Errorf("error-level target got %d records, want 0 for an INFO line", len(*quiet.records))
	}
}

func TestFanoutHandler_Enabled(t *testing.T) {
	t.Parallel()

	h := newFanoutHandler(newRecordingHandler(slog.LevelError, nil), newRecordingHandler(slog.LevelWarn, nil))
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Enabled(WARN) = false, want true when any target accepts it")
	}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(INFO) = true, want false when no target accepts it")
	}
}

// TestFanoutHandler_oneBrokenTargetDoesNotStopTheOthers is the property
// the whole type exists for: a failing OTel exporter must not cost the
// operator their terminal output.
func TestFanoutHandler_oneBrokenTargetDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	boom := errors.New("exporter down")
	broken := newRecordingHandler(slog.LevelDebug, boom)
	healthy := newRecordingHandler(slog.LevelDebug, nil)
	h := newFanoutHandler(broken, healthy)

	err := h.Handle(context.Background(), slog.Record{Level: slog.LevelInfo})
	if !errors.Is(err, boom) {
		t.Errorf("Handle = %v, want the target's own error", err)
	}
	if len(*healthy.records) != 1 {
		t.Errorf("healthy target got %d records, want 1", len(*healthy.records))
	}
}

func TestFanoutHandler_WithAttrsAndWithGroup(t *testing.T) {
	t.Parallel()

	a := newRecordingHandler(slog.LevelDebug, nil)
	b := newRecordingHandler(slog.LevelDebug, nil)
	h := newFanoutHandler(a, b).WithAttrs([]slog.Attr{slog.String("k", "v")}).WithGroup("g")

	if len(*a.attrs) != 1 || len(*b.attrs) != 1 {
		t.Errorf("WithAttrs reached %d/%d targets, want 1/1", len(*a.attrs), len(*b.attrs))
	}
	if len(*a.groups) != 1 || len(*b.groups) != 1 {
		t.Errorf("WithGroup reached %d/%d targets, want 1/1", len(*a.groups), len(*b.groups))
	}
	if err := h.Handle(context.Background(), slog.Record{Level: slog.LevelInfo}); err != nil {
		t.Errorf("Handle after WithAttrs/WithGroup = %v", err)
	}
}
