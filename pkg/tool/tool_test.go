package tool_test

import (
	"testing"

	"github.com/pluggableharness/agent/pkg/tool"
)

func TestKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind tool.Kind
		want string
	}{
		{"unspecified", tool.KindUnspecified, "unspecified"},
		{"resource", tool.KindResource, "resource"},
		{"data_source", tool.KindDataSource, "data_source"},
		{"interactive", tool.KindInteractive, "interactive"},
		{"out of range", tool.Kind(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestRiskClassString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		risk tool.RiskClass
		want string
	}{
		{"unspecified", tool.RiskClassUnspecified, "unspecified"},
		{"read_only", tool.RiskClassReadOnly, "read_only"},
		{"low", tool.RiskClassLow, "low"},
		{"moderate", tool.RiskClassModerate, "moderate"},
		{"high", tool.RiskClassHigh, "high"},
		{"critical", tool.RiskClassCritical, "critical"},
		{"out of range", tool.RiskClass(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.risk.String(); got != tt.want {
				t.Errorf("RiskClass(%d).String() = %q, want %q", tt.risk, got, tt.want)
			}
		})
	}
}

func TestOutputStreamString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stream tool.OutputStream
		want   string
	}{
		{"unspecified", tool.OutputStreamUnspecified, "unspecified"},
		{"stdout", tool.OutputStreamStdout, "stdout"},
		{"stderr", tool.OutputStreamStderr, "stderr"},
		{"out of range", tool.OutputStream(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.stream.String(); got != tt.want {
				t.Errorf("OutputStream(%d).String() = %q, want %q", tt.stream, got, tt.want)
			}
		})
	}
}

func TestNewOutputChunkEvent(t *testing.T) {
	t.Parallel()

	ev := tool.NewOutputChunkEvent(tool.OutputStreamStdout, []byte("hello"))
	if ev.OutputChunk == nil {
		t.Fatal("NewOutputChunkEvent: OutputChunk field is nil")
	}
	if ev.OutputChunk.Stream != tool.OutputStreamStdout {
		t.Errorf("OutputChunk.Stream = %v, want %v", ev.OutputChunk.Stream, tool.OutputStreamStdout)
	}
	if string(ev.OutputChunk.Data) != "hello" {
		t.Errorf("OutputChunk.Data = %q, want %q", ev.OutputChunk.Data, "hello")
	}
	if ev.Progress != nil || ev.PartialResult != nil || ev.ExitStatus != nil || ev.Result != nil || ev.Error != nil {
		t.Errorf("NewOutputChunkEvent set more than one field: %+v", ev)
	}
}

func TestNewProgressEvent(t *testing.T) {
	t.Parallel()

	frac := 0.5
	ev := tool.NewProgressEvent("halfway", &frac)
	if ev.Progress == nil {
		t.Fatal("NewProgressEvent: Progress field is nil")
	}
	if ev.Progress.Message != "halfway" {
		t.Errorf("Progress.Message = %q, want %q", ev.Progress.Message, "halfway")
	}
	if ev.Progress.FractionComplete == nil || *ev.Progress.FractionComplete != 0.5 {
		t.Errorf("Progress.FractionComplete = %v, want 0.5", ev.Progress.FractionComplete)
	}
}

func TestNewPartialResultEvent(t *testing.T) {
	t.Parallel()

	ev := tool.NewPartialResultEvent(map[string]any{"hit": "a.go"})
	if ev.PartialResult == nil {
		t.Fatal("NewPartialResultEvent: PartialResult field is nil")
	}
	if ev.PartialResult.Payload["hit"] != "a.go" {
		t.Errorf("PartialResult.Payload = %v, want hit=a.go", ev.PartialResult.Payload)
	}
}

func TestNewExitStatusEvent(t *testing.T) {
	t.Parallel()

	sig := "SIGTERM"
	ev := tool.NewExitStatusEvent(1, &sig)
	if ev.ExitStatus == nil {
		t.Fatal("NewExitStatusEvent: ExitStatus field is nil")
	}
	if ev.ExitStatus.ExitCode != 1 {
		t.Errorf("ExitStatus.ExitCode = %d, want 1", ev.ExitStatus.ExitCode)
	}
	if ev.ExitStatus.Signal == nil || *ev.ExitStatus.Signal != "SIGTERM" {
		t.Errorf("ExitStatus.Signal = %v, want SIGTERM", ev.ExitStatus.Signal)
	}
}

func TestNewResultEvent(t *testing.T) {
	t.Parallel()

	ev := tool.NewResultEvent(map[string]any{"ok": true})
	if ev.Result == nil {
		t.Fatal("NewResultEvent: Result field is nil")
	}
	if ev.Result.Payload["ok"] != true {
		t.Errorf("Result.Payload = %v, want ok=true", ev.Result.Payload)
	}
}

func TestNewErrorEvent(t *testing.T) {
	t.Parallel()

	te, err := tool.NewError(tool.ErrorCategoryNotFound, "not found", false, nil)
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	ev := tool.NewErrorEvent(te)
	if ev.Error != te {
		t.Errorf("NewErrorEvent: Error field = %v, want %v", ev.Error, te)
	}
}
