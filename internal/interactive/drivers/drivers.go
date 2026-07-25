// Package drivers is the driver selector for internal/interactive
// (go-layout.md's driver pattern): the sole place that switches on an
// interactive-resolver driver name. Adding a driver — notably the
// spec-correct frontend-backed one, once a frontend attach path exists —
// means adding a sub-package here plus one line in New's switch, and
// nothing else in the kernel branching on a driver name.
package drivers

import (
	"fmt"
	"log/slog"

	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/interactive/drivers/unattended"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// ErrUnknownDriver is returned by New for a name outside the known set,
// including the empty string — there is deliberately no default driver.
// Refusing every interactive call is a defensible behavior to select on
// purpose, and an indefensible one to fall into by omitting a name.
var ErrUnknownDriver = fmt.Errorf("interactive: drivers: unknown driver")

// New returns the interactive.Resolver named by name, wired to logger and
// telem. The only recognized name today is "unattended" — the tracked
// deviation standing in for the frontend round trip
// (agent-loop/plan-apply-gate.md#data-source-and-interactive-calls) until
// a frontend attach path exists.
//
// drivers/fake is deliberately not registered here: it is scripted
// per-test with a Response and an error that this signature has no way to
// carry, so tests construct it directly rather than through the selector.
func New(name string, logger *slog.Logger, telem *telemetry.Provider) (interactive.Resolver, error) {
	switch name {
	case "unattended":
		return unattended.New(logger, telem), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownDriver, name)
	}
}
