package drivers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/autoallow"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNew_autoAllowUnsafe(t *testing.T) {
	t.Parallel()

	r, err := drivers.New(drivers.NameAutoAllowUnsafe, drivers.Config{
		AcknowledgeUnsafeAutoAllow: true,
		Logger:                     quietLogger(),
	})
	if err != nil {
		t.Fatalf("New(%q): %v", drivers.NameAutoAllowUnsafe, err)
	}
	if r == nil {
		t.Fatalf("New(%q) returned a nil Resolver with a nil error", drivers.NameAutoAllowUnsafe)
	}

	got, err := r.Resolve(context.Background(), plandecision.Request{
		SessionID: "sess-1",
		Item:      &planv1.PlanItem{Id: "pi-1", Provider: "filesystem", OperationName: "write_file"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DecidedBy != autoallow.DecidedBy {
		t.Errorf("DecidedBy = %q, want %q — the selector wired up something other than autoallow", got.DecidedBy, autoallow.DecidedBy)
	}
}

func TestNew_autoAllowUnsafeRequiresAcknowledgement(t *testing.T) {
	t.Parallel()

	// Selecting the driver by name is not itself the acknowledgement —
	// the caller must still opt in, in code.
	_, err := drivers.New(drivers.NameAutoAllowUnsafe, drivers.Config{Logger: quietLogger()})
	if !errors.Is(err, autoallow.ErrNotAcknowledged) {
		t.Fatalf("New: error = %v, want errors.Is autoallow.ErrNotAcknowledged", err)
	}
}

func TestNew_noDefaultDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		driverName string
	}{
		{name: "empty name", driverName: ""},
		{name: "unrecognized name", driverName: "does-not-exist"},
		{name: "package name is not the driver name", driverName: "autoallow"},
		{name: "reserved but unimplemented", driverName: "frontend"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := drivers.New(tt.driverName, drivers.Config{AcknowledgeUnsafeAutoAllow: true})
			if !errors.Is(err, drivers.ErrUnknownDriver) {
				t.Fatalf("New(%q): error = %v, want errors.Is ErrUnknownDriver", tt.driverName, err)
			}
			if r != nil {
				t.Fatalf("New(%q) returned a non-nil Resolver alongside an error", tt.driverName)
			}
		})
	}
}
