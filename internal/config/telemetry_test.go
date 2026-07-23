package config

import (
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/codes"
)

// fakeLogHandler is a hand-written slog.Handler fake (go-testing.md: fakes,
// not mocking frameworks) that captures every Record it receives, for
// asserting on LoadFile's DEBUG entry log without a real log sink.
type fakeLogHandler struct {
	records []slog.Record
}

func (h *fakeLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *fakeLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *fakeLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *fakeLogHandler) WithGroup(string) slog.Handler      { return h }

// TestLoadFile_recordsSpanAndDebugLog asserts LoadFile's internal/CLAUDE.md
// instrumentation: exactly one config.load span recorded via the fake
// telemetry driver, and a DEBUG entry log line carrying the file path,
// with no decoded config content in the log attributes.
func TestLoadFile_recordsSpanAndDebugLog(t *testing.T) {
	prov, backend := testProviderWithBackend(t)

	handler := &fakeLogHandler{}
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	path := writeHCL(t, minimalValidHCL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-123")

	if _, err := LoadFile(context.Background(), prov, path); err != nil {
		t.Fatalf("LoadFile: unexpected error: %v", err)
	}

	if err := prov.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	spans := backend.Spans.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != "config.load" {
		t.Errorf("span Name = %q, want config.load", got)
	}
	if got := spans[0].Status.Code; got != codes.Ok {
		t.Errorf("span Status = %v, want Ok", got)
	}

	var debugRecord *slog.Record
	for i := range handler.records {
		if handler.records[i].Level == slog.LevelDebug {
			debugRecord = &handler.records[i]
			break
		}
	}
	if debugRecord == nil {
		t.Fatal("no DEBUG log record captured for LoadFile")
	}
	if got := debugRecord.Message; got != "config: loading file" {
		t.Errorf("DEBUG message = %q, want %q", got, "config: loading file")
	}
	var gotPath string
	var attrCount int
	debugRecord.Attrs(func(a slog.Attr) bool {
		attrCount++
		if a.Key == "path" {
			gotPath = a.Value.String()
		}
		return true
	})
	if gotPath != path {
		t.Errorf("DEBUG log path attr = %q, want %q", gotPath, path)
	}
	if attrCount != 1 {
		t.Errorf("DEBUG log has %d attrs, want exactly 1 (path only, no decoded config content)", attrCount)
	}
}
