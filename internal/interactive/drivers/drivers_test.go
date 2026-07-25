package drivers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/interactive/drivers/unattended"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
)

// discardLogger keeps the selector's construction-time INFO out of test
// output — the log's content is unattended's own test's concern.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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

func TestNew_unattended(t *testing.T) {
	t.Parallel()

	got, err := New("unattended", discardLogger(), newTestProvider(t))
	if err != nil {
		t.Fatalf("New(%q): %v", "unattended", err)
	}
	if _, ok := got.(*unattended.Resolver); !ok {
		t.Fatalf("New(%q) returned %T, want *unattended.Resolver", "unattended", got)
	}
	if _, err := got.Resolve(context.Background(), interactive.Request{ToolName: "ask_user"}); !errors.Is(err, interactive.ErrNoFrontend) {
		t.Errorf("Resolve error = %v, want errors.Is interactive.ErrNoFrontend", err)
	}
}

func TestNew_unknownDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		driverName string
	}{
		// The empty name is an error on purpose: there is no default
		// driver, because refusing every interactive call is defensible
		// to select deliberately and indefensible to fall into.
		{name: "empty", driverName: ""},
		{name: "unknown", driverName: "nope"},
		{name: "wrong case", driverName: "Unattended"},
		// Registered nowhere: the fake is scripted per-test and
		// constructed directly, never through the selector.
		{name: "fake", driverName: "fake"},
		{name: "not yet built", driverName: "frontend"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := New(tt.driverName, discardLogger(), newTestProvider(t))
			if !errors.Is(err, ErrUnknownDriver) {
				t.Fatalf("New(%q) error = %v, want errors.Is ErrUnknownDriver", tt.driverName, err)
			}
			if got != nil {
				t.Errorf("New(%q) = %v, want nil alongside an error", tt.driverName, got)
			}
		})
	}
}
