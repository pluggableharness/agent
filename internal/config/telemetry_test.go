package config

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers"
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

// loudObservability is a fully-populated, deliberately non-default
// Observability, used to prove telemetry = false discards it entirely
// rather than letting any of it reach an exporter.
var loudObservability = Observability{
	Endpoint:         "collector.example:4317",
	Protocol:         "grpc",
	SamplingRatio:    0.25,
	TracesEnabled:    true,
	MetricsEnabled:   true,
	LogsEnabled:      true,
	ExportIntervalMS: 2500,
	ServiceName:      "kernel",
	ResourceAttrs:    map[string]string{"env": "prod"},
}

func TestTelemetryConfig(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
		want     telemetry.Config
	}{
		{
			// settings-and-global.md#the-telemetry-switch: telemetry =
			// false MUST wire a discarding backend regardless of what
			// observability{} declared — no exporter is ever constructed.
			name:     "telemetry off forces the noop backend",
			settings: Settings{Telemetry: false, Observability: loudObservability},
			want: telemetry.Config{
				Enabled:        false,
				Backend:        "noop",
				Endpoint:       "collector.example:4317",
				SamplingRatio:  0.25,
				TracesEnabled:  true,
				MetricsEnabled: true,
				LogsEnabled:    true,
				ExportInterval: 2500 * time.Millisecond,
				ServiceName:    "kernel",
				ResourceAttrs:  map[string]string{"env": "prod"},
			},
		},
		{
			name:     "telemetry on with observability declared, grpc",
			settings: Settings{Telemetry: true, Observability: loudObservability},
			want: telemetry.Config{
				Enabled:        true,
				Backend:        "otlpgrpc",
				Endpoint:       "collector.example:4317",
				SamplingRatio:  0.25,
				TracesEnabled:  true,
				MetricsEnabled: true,
				LogsEnabled:    true,
				ExportInterval: 2500 * time.Millisecond,
				ServiceName:    "kernel",
				ResourceAttrs:  map[string]string{"env": "prod"},
			},
		},
		{
			name: "http protocol selects the otlphttp driver",
			settings: Settings{
				Telemetry: true,
				Observability: Observability{
					Endpoint:         "https://collector.example",
					Protocol:         "http",
					SamplingRatio:    1.0,
					TracesEnabled:    true,
					MetricsEnabled:   false,
					LogsEnabled:      true,
					ExportIntervalMS: 1000,
					ServiceName:      "kernel",
				},
			},
			want: telemetry.Config{
				Enabled:        true,
				Backend:        "otlphttp",
				Endpoint:       "https://collector.example",
				SamplingRatio:  1.0,
				TracesEnabled:  true,
				MetricsEnabled: false,
				LogsEnabled:    true,
				ExportInterval: 1000 * time.Millisecond,
				ServiceName:    "kernel",
			},
		},
		{
			// The observability{}-absent path: LoadFile hands
			// DefaultObservability through, which must land on
			// telemetry.DefaultConfig's own values.
			name:     "telemetry on with observability absent uses DefaultObservability",
			settings: Settings{Telemetry: true, Observability: DefaultObservability},
			want: telemetry.Config{
				Enabled:        true,
				Backend:        "otlpgrpc",
				Endpoint:       "localhost:4317",
				SamplingRatio:  1.0,
				TracesEnabled:  true,
				MetricsEnabled: true,
				LogsEnabled:    true,
				ExportInterval: 10 * time.Second,
				ServiceName:    "pluggableharness-agent",
			},
		},
		{
			// A protocol outside {grpc, http} can only arise from a
			// hand-built Settings (LoadFile rejects it at decode time), and
			// yields a deliberately invalid backend name so drivers.New
			// fails loudly instead of guessing a transport.
			name: "unknown protocol yields an unusable backend name",
			settings: Settings{
				Telemetry:     true,
				Observability: Observability{Protocol: "carrier-pigeon", ServiceName: "kernel"},
			},
			want: telemetry.Config{Enabled: true, Backend: "", ServiceName: "kernel"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := TelemetryConfig(tt.settings)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("TelemetryConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTelemetryConfig_backendNamesAreRealDrivers pins the mapping against
// drivers.New's actual name set rather than against string literals
// duplicated in this test, so a driver rename can't leave this bridge
// silently producing an unroutable backend name.
func TestTelemetryConfig_backendNamesAreRealDrivers(t *testing.T) {
	t.Parallel()

	settings := []Settings{
		{Telemetry: true, Observability: Observability{Protocol: "grpc", ServiceName: "kernel"}},
		{Telemetry: true, Observability: Observability{Protocol: "http", ServiceName: "kernel"}},
		{Telemetry: false, Observability: loudObservability},
	}
	for _, s := range settings {
		cfg := TelemetryConfig(s)
		if _, err := drivers.New(cfg.Backend, cfg); err != nil {
			t.Errorf("drivers.New(%q): %v", cfg.Backend, err)
		}
	}

	// The converse: the unknown-protocol escape hatch really is rejected.
	bad := TelemetryConfig(Settings{Telemetry: true, Observability: Observability{Protocol: "carrier-pigeon"}})
	if _, err := drivers.New(bad.Backend, bad); err == nil {
		t.Errorf("drivers.New(%q): want ErrUnknownDriver, got nil", bad.Backend)
	}
}

// TestTelemetryConfig_fromLoadedFile is the end-to-end shape: an agent.hcl
// with telemetry = false and a populated observability{} still bridges to
// the discarding backend.
func TestTelemetryConfig_fromLoadedFile(t *testing.T) {
	t.Parallel()

	path := writeHCL(t, `
settings {
  default_frontend = "tui"
  log_level        = "info"
  telemetry        = false

  observability {
    endpoint            = "collector.example:4317"
    protocol            = "grpc"
    sampling_ratio      = 0.5
    traces_enabled      = true
    metrics_enabled     = true
    logs_enabled        = true
    export_interval_ms  = 5000
    service_name        = "kernel"
  }
}
`)
	cfg, err := LoadFile(context.Background(), testProvider(t), path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	tel := TelemetryConfig(cfg.Settings)
	if tel.Enabled {
		t.Error("Enabled = true, want false")
	}
	if tel.Backend != "noop" {
		t.Errorf("Backend = %q, want noop despite observability{} declaring grpc", tel.Backend)
	}
	if err := tel.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

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
