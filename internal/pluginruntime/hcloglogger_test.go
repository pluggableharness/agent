package pluginruntime

import (
	"log/slog"
	"testing"

	"github.com/hashicorp/go-hclog"

	internallog "github.com/pluggableharness/agent/internal/log"
)

func TestSlogLevelFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   hclog.Level
		want slog.Level
	}{
		{"trace", hclog.Trace, internallog.LevelTrace},
		{"debug", hclog.Debug, slog.LevelDebug},
		{"info", hclog.Info, slog.LevelInfo},
		{"warn", hclog.Warn, slog.LevelWarn},
		{"error", hclog.Error, slog.LevelError},
		{"noLevel", hclog.NoLevel, slog.LevelInfo},
		{"off", hclog.Off, slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := slogLevelFor(tt.in); got != tt.want {
				t.Errorf("slogLevelFor(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHclogAdapter_leveledMethods(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{minLevel: internallog.LevelTrace}
	logger := newHCLogger(slog.New(h), "test")

	logger.Trace("trace msg", "k", "v1")
	logger.Debug("debug msg", "k", "v2")
	logger.Info("info msg", "k", "v3")
	logger.Warn("warn msg", "k", "v4")
	logger.Error("error msg", "k", "v5")

	wantLevels := []slog.Level{internallog.LevelTrace, slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	if len(h.records) != len(wantLevels) {
		t.Fatalf("captured %d records, want %d", len(h.records), len(wantLevels))
	}
	for i, want := range wantLevels {
		if h.records[i].Level != want {
			t.Errorf("record[%d].Level = %v, want %v", i, h.records[i].Level, want)
		}
	}
	if attrs := collectAttrs(h.records[2]); attrs["k"] != "v3" {
		t.Errorf("record[2] attrs[k] = %v, want v3", attrs["k"])
	}
}

func TestHclogAdapter_Log(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{}
	logger := newHCLogger(slog.New(h), "test")
	logger.Log(hclog.Warn, "generic", "a", 1)

	if len(h.records) != 1 {
		t.Fatalf("captured %d records, want 1", len(h.records))
	}
	if h.records[0].Level != slog.LevelWarn {
		t.Errorf("Level = %v, want %v", h.records[0].Level, slog.LevelWarn)
	}
	if h.records[0].Message != "generic" {
		t.Errorf("Message = %q, want %q", h.records[0].Message, "generic")
	}
}

func TestHclogAdapter_isGuards(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{minLevel: slog.LevelWarn}
	logger := newHCLogger(slog.New(h), "test")

	if logger.IsTrace() {
		t.Error("IsTrace() = true, want false (below minLevel)")
	}
	if logger.IsDebug() {
		t.Error("IsDebug() = true, want false (below minLevel)")
	}
	if logger.IsInfo() {
		t.Error("IsInfo() = true, want false (below minLevel)")
	}
	if !logger.IsWarn() {
		t.Error("IsWarn() = false, want true (at minLevel)")
	}
	if !logger.IsError() {
		t.Error("IsError() = false, want true (above minLevel)")
	}
}

func TestHclogAdapter_withAndImpliedArgs(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{}
	logger := newHCLogger(slog.New(h), "test")

	sub := logger.With("request_id", "abc")
	if got := sub.ImpliedArgs(); len(got) != 2 || got[0] != "request_id" || got[1] != "abc" {
		t.Errorf("ImpliedArgs() = %v, want [request_id abc]", got)
	}
	// The base logger's own ImpliedArgs must stay untouched.
	if got := logger.ImpliedArgs(); len(got) != 0 {
		t.Errorf("base ImpliedArgs() = %v, want empty", got)
	}

	// The sublogger still logs through to the same underlying handler
	// (fakeHandler.WithAttrs is a no-op passthrough, per this repo's
	// existing fakeHandler convention — the implied args' propagation
	// into the *slog.Logger chain is exercised above via ImpliedArgs;
	// this only confirms the sublogger keeps logging at all).
	sub.Info("via sublogger")
	if len(h.records) != 1 {
		t.Fatalf("captured %d records, want 1", len(h.records))
	}
	if h.records[0].Message != "via sublogger" {
		t.Errorf("Message = %q, want %q", h.records[0].Message, "via sublogger")
	}
}

func TestHclogAdapter_nameAndNamed(t *testing.T) {
	t.Parallel()

	logger := newHCLogger(slog.New(&fakeHandler{}), "root")
	if logger.Name() != "root" {
		t.Fatalf("Name() = %q, want %q", logger.Name(), "root")
	}

	child := logger.Named("sub")
	if child.Name() != "root.sub" {
		t.Errorf("Named: Name() = %q, want %q", child.Name(), "root.sub")
	}

	reset := logger.ResetNamed("other")
	if reset.Name() != "other" {
		t.Errorf("ResetNamed: Name() = %q, want %q", reset.Name(), "other")
	}

	// Named on an unnamed logger should not produce a leading dot.
	unnamed := newHCLogger(slog.New(&fakeHandler{}), "")
	if got := unnamed.Named("x").Name(); got != "x" {
		t.Errorf("Named on unnamed logger: Name() = %q, want %q", got, "x")
	}
}

func TestHclogAdapter_setGetLevel(t *testing.T) {
	t.Parallel()

	logger := newHCLogger(slog.New(&fakeHandler{}), "test")
	if got := logger.GetLevel(); got != hclog.Info {
		t.Fatalf("GetLevel() default = %v, want %v", got, hclog.Info)
	}

	logger.SetLevel(hclog.Debug)
	if got := logger.GetLevel(); got != hclog.Debug {
		t.Errorf("GetLevel() after SetLevel(Debug) = %v, want %v", got, hclog.Debug)
	}

	// A sublogger inherits the level at the moment it was created.
	logger.SetLevel(hclog.Warn)
	sub := logger.With("k", "v")
	if got := sub.GetLevel(); got != hclog.Warn {
		t.Errorf("sublogger GetLevel() = %v, want %v", got, hclog.Warn)
	}
}

func TestHclogAdapter_standardLoggerAndWriter(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{}
	logger := newHCLogger(slog.New(h), "test")

	w := logger.StandardWriter(&hclog.StandardLoggerOptions{ForceLevel: hclog.Error})
	n, err := w.Write([]byte("boom\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("boom\n") {
		t.Errorf("Write: n = %d, want %d", n, len("boom\n"))
	}
	if len(h.records) != 1 {
		t.Fatalf("captured %d records, want 1", len(h.records))
	}
	if h.records[0].Level != slog.LevelError {
		t.Errorf("Level = %v, want %v", h.records[0].Level, slog.LevelError)
	}
	if h.records[0].Message != "boom" {
		t.Errorf("Message = %q, want %q", h.records[0].Message, "boom")
	}

	// An empty (whitespace-only-after-trim) line writes nothing.
	h.records = nil
	if _, err := w.Write([]byte("\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(h.records) != 0 {
		t.Errorf("captured %d records for a blank line, want 0", len(h.records))
	}

	// StandardLogger without a forced level defaults to Info.
	h.records = nil
	stdLogger := logger.StandardLogger(nil)
	stdLogger.Print("via stdlib")
	if len(h.records) != 1 {
		t.Fatalf("captured %d records, want 1", len(h.records))
	}
	if h.records[0].Level != slog.LevelInfo {
		t.Errorf("Level = %v, want %v", h.records[0].Level, slog.LevelInfo)
	}
}

func TestNewHCLogger_satisfiesInterface(t *testing.T) {
	t.Parallel()

	var _ hclog.Logger = newHCLogger(slog.New(&fakeHandler{}), "test")
}
