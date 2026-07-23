package drivers_test

import (
	"errors"
	"testing"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers"
)

func TestNew(t *testing.T) {
	t.Parallel()

	cfg := telemetry.DefaultConfig
	cfg.Endpoint = "localhost:4317"

	tests := []struct {
		name     string
		wantName string
	}{
		{"otlpgrpc", "otlpgrpc"},
		{"otlphttp", "otlphttp"},
		{"stdout", "stdout"},
		{"noop", "noop"},
		{"fake", "fake"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backend, err := drivers.New(tt.name, cfg)
			if err != nil {
				t.Fatalf("New(%q): %v", tt.name, err)
			}
			if backend == nil {
				t.Fatalf("New(%q) returned nil backend with a nil error", tt.name)
			}
			if got := backend.Name(); got != tt.wantName {
				t.Errorf("Name() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestNew_unknownDriver(t *testing.T) {
	t.Parallel()

	_, err := drivers.New("does-not-exist", telemetry.DefaultConfig)
	if !errors.Is(err, drivers.ErrUnknownDriver) {
		t.Fatalf("New: error = %v, want errors.Is ErrUnknownDriver", err)
	}
}
