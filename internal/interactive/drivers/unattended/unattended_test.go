package unattended

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// fakeHandler is a hand-written slog.Handler fake (go-testing.md: fakes,
// not mocking frameworks) capturing every Record it receives, mirroring
// internal/log's and internal/pluginruntime's own fakeHandler.
type fakeHandler struct {
	records []slog.Record
}

func (h *fakeHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *fakeHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *fakeHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *fakeHandler) WithGroup(string) slog.Handler      { return h }

// recordsAt returns every captured Record at exactly level.
func (h *fakeHandler) recordsAt(level slog.Level) []slog.Record {
	var out []slog.Record
	for _, r := range h.records {
		if r.Level == level {
			out = append(out, r)
		}
	}
	return out
}

// collectAttrs flattens a Record's attributes into a map, for assertions.
func collectAttrs(r slog.Record) map[string]any {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

// newTestProvider builds a fully-disabled telemetry.Provider — the
// instrumentation code path still runs, it just exports nowhere.
func newTestProvider(t *testing.T) *telemetry.Provider {
	t.Helper()

	prov, err := telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.Shutdown(context.Background()); err != nil {
			t.Errorf("Provider.Shutdown: %v", err)
		}
	})
	return prov
}

func TestNew(t *testing.T) {
	t.Parallel()

	handler := &fakeHandler{}
	r := New(slog.New(handler), nil)

	if r == nil {
		t.Fatal("New returned nil")
	}
	if r.Logger == nil {
		t.Error("New: Logger is nil, want the supplied logger")
	}
	if r.Telemetry != nil {
		t.Error("New: Telemetry is non-nil, want the nil that was passed through unchanged")
	}

	infos := handler.recordsAt(slog.LevelInfo)
	if len(infos) != 1 {
		t.Fatalf("New: INFO records = %d, want exactly 1", len(infos))
	}
	// Construction is INFO, never WARN: refusing interactive calls in a
	// build with no frontend is the safe expected behavior, not a risk.
	if warns := handler.recordsAt(slog.LevelWarn); len(warns) != 0 {
		t.Errorf("New: WARN records = %d, want 0", len(warns))
	}
	if got := collectAttrs(infos[0])["driver"]; got != driverName {
		t.Errorf("New: INFO driver attr = %v, want %q", got, driverName)
	}
}

func TestNew_nilLogger(t *testing.T) {
	// Not parallel: slog.Default() is process-global state.
	handler := &fakeHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := New(nil, nil)
	if r.Logger == nil {
		t.Fatal("New(nil, nil): Logger is nil, want the slog.Default() fallback")
	}
	if len(handler.recordsAt(slog.LevelInfo)) != 1 {
		t.Errorf("New(nil, nil): INFO records = %d, want exactly 1 through slog.Default()", len(handler.recordsAt(slog.LevelInfo)))
	}
}

func TestResolve_alwaysRefuses(t *testing.T) {
	t.Parallel()

	args, err := structpb.NewStruct(map[string]any{"question": "proceed?"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	tests := []struct {
		name string
		req  interactive.Request
	}{
		{
			name: "zero request",
			req:  interactive.Request{},
		},
		{
			name: "fully populated request",
			req: interactive.Request{
				CallID:    "call-1",
				ToolName:  "ask_user",
				Arguments: args,
				Prompt:    &renderv1.RenderTree{},
			},
		},
		{
			name: "no prompt",
			req:  interactive.Request{CallID: "call-2", ToolName: "confirm_deploy"},
		},
		{
			name: "no arguments",
			req:  interactive.Request{CallID: "call-3", ToolName: "ask_user", Prompt: &renderv1.RenderTree{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &fakeHandler{}
			r := New(slog.New(handler), newTestProvider(t))

			resp, err := r.Resolve(context.Background(), tt.req)
			if !errors.Is(err, interactive.ErrNoFrontend) {
				t.Fatalf("Resolve error = %v, want errors.Is interactive.ErrNoFrontend", err)
			}
			if resp.Payload != nil {
				t.Errorf("Resolve payload = %v, want nil — a refusal must never fabricate an answer", resp.Payload)
			}

			warns := handler.recordsAt(slog.LevelWarn)
			if len(warns) != 1 {
				t.Fatalf("Resolve: WARN records = %d, want exactly 1 per call", len(warns))
			}
			attrs := collectAttrs(warns[0])
			if got := attrs["tool_name"]; got != tt.req.ToolName {
				t.Errorf("Resolve: WARN tool_name attr = %v, want %q", got, tt.req.ToolName)
			}
			if got := attrs["call_id"]; got != tt.req.CallID {
				t.Errorf("Resolve: WARN call_id attr = %v, want %q", got, tt.req.CallID)
			}
			if len(handler.recordsAt(slog.LevelDebug)) != 1 {
				t.Errorf("Resolve: DEBUG records = %d, want exactly 1 entry log", len(handler.recordsAt(slog.LevelDebug)))
			}
		})
	}
}

func TestResolve_repeatedCallsWarnEachTime(t *testing.T) {
	t.Parallel()

	handler := &fakeHandler{}
	r := New(slog.New(handler), newTestProvider(t))

	const calls = 3
	for i := range calls {
		if _, err := r.Resolve(context.Background(), interactive.Request{CallID: "call", ToolName: "ask_user"}); !errors.Is(err, interactive.ErrNoFrontend) {
			t.Fatalf("Resolve #%d error = %v, want errors.Is interactive.ErrNoFrontend", i, err)
		}
	}

	if got := len(handler.recordsAt(slog.LevelWarn)); got != calls {
		t.Errorf("WARN records = %d, want %d — one per refusal, since repeated refusals are the signal", got, calls)
	}
}

func TestResolve_nilTelemetry(t *testing.T) {
	t.Parallel()

	handler := &fakeHandler{}
	r := New(slog.New(handler), nil)

	if _, err := r.Resolve(context.Background(), interactive.Request{ToolName: "ask_user"}); !errors.Is(err, interactive.ErrNoFrontend) {
		t.Fatalf("Resolve error = %v, want errors.Is interactive.ErrNoFrontend", err)
	}
	if got := len(handler.recordsAt(slog.LevelWarn)); got != 1 {
		t.Errorf("WARN records = %d, want 1 even with instrumentation off", got)
	}
}

func TestResolve_zeroValueResolver(t *testing.T) {
	// Not parallel: exercises the slog.Default() fallback on a Resolver
	// assembled by hand rather than via New.
	handler := &fakeHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var r Resolver
	if _, err := r.Resolve(context.Background(), interactive.Request{ToolName: "ask_user"}); !errors.Is(err, interactive.ErrNoFrontend) {
		t.Fatalf("Resolve error = %v, want errors.Is interactive.ErrNoFrontend", err)
	}
	if got := len(handler.recordsAt(slog.LevelWarn)); got != 1 {
		t.Errorf("WARN records = %d, want 1 through slog.Default()", got)
	}
}

func TestResolve_canceledContextWinsOverRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     func(t *testing.T) context.Context
		wantErr error
	}{
		{
			name: "canceled",
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline exceeded",
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
			wantErr: context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &fakeHandler{}
			r := New(slog.New(handler), newTestProvider(t))

			resp, err := r.Resolve(tt.ctx(t), interactive.Request{CallID: "call-1", ToolName: "ask_user"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Resolve error = %v, want errors.Is %v", err, tt.wantErr)
			}
			// Cancellation is checked first, so the refusal never happens
			// and never gets logged or reported as the reason.
			if errors.Is(err, interactive.ErrNoFrontend) {
				t.Error("Resolve error is ErrNoFrontend, want the context error to win")
			}
			if resp.Payload != nil {
				t.Errorf("Resolve payload = %v, want nil", resp.Payload)
			}
			if got := len(handler.recordsAt(slog.LevelWarn)); got != 0 {
				t.Errorf("WARN records = %d, want 0 — a canceled call is not a refusal", got)
			}
		})
	}
}
