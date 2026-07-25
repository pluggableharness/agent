// Package drivers is the driver selector for internal/plandecision
// (go-layout.md's driver pattern): the sole place that switches on a
// plan-decision resolver name.
//
// Two things about this selector are deliberate and load-bearing, not
// stylistic:
//
//   - There is NO default driver name. An empty name is
//     ErrUnknownDriver, exactly like a misspelled one, so nothing can
//     silently fall back to the auto-allow resolver by omission. A build
//     that wants auto-allow has to name it — and then acknowledge it
//     again via Config.AcknowledgeUnsafeAutoAllow.
//   - The registered name is "auto-allow-unsafe", not "autoallow", so the
//     name itself reads as a warning wherever it appears — in an
//     agent.hcl, in a log line, in a code review diff.
//
// The name "frontend" is reserved here for the future spec-correct
// driver: the one that emits a permission-request ServerEvent and blocks
// on the matching ClientEvent.plan_decision
// ([docs/specifications/agent-loop/plan-apply-gate.md#decision-semantics]).
// It is deliberately not stubbed — an unimplemented name must fail
// construction, not return something that pretends to ask.
//
// The test-only fake (drivers/fake) is deliberately NOT registered:
// scripting it requires per-call responses that have no representation in
// Config, and a selectable fake would be one more way to obtain a
// resolver that never asks a human. Tests construct it directly.
package drivers

import (
	"fmt"
	"log/slog"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/autoallow"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// Driver names this selector recognizes.
const (
	// NameAutoAllowUnsafe selects internal/plandecision/drivers/autoallow
	// — the tracked deviation that auto-approves every ask decision. Read
	// that package's CLAUDE.md before selecting it.
	NameAutoAllowUnsafe = "auto-allow-unsafe"
)

// ErrUnknownDriver is returned by New for any name outside the known set,
// including the empty string — "unknown driver name" is a concept of the
// selector, not of the plandecision.Resolver interface itself.
var ErrUnknownDriver = fmt.Errorf("plandecision: drivers: unknown driver")

// Config carries everything any driver's own New needs, passed through
// uniformly regardless of which driver a given name selects (matching
// internal/telemetry/drivers' uniform-signature convention).
type Config struct {
	// AcknowledgeUnsafeAutoAllow is passed through to
	// autoallow.Config.AcknowledgeUnsafeAutoAllow. Naming
	// NameAutoAllowUnsafe without setting this fails with
	// autoallow.ErrNotAcknowledged: selecting the driver by name is not
	// itself the acknowledgement.
	AcknowledgeUnsafeAutoAllow bool
	// Logger is the slog.Logger the selected driver logs through. Nil
	// leaves the driver's own default.
	Logger *slog.Logger
	// Telemetry is the Provider the selected driver instruments through.
	// Nil leaves the driver's own default.
	Telemetry *telemetry.Provider
}

// New returns the plandecision.Resolver named by name, configured from
// cfg, or ErrUnknownDriver. There is no default: an empty or unrecognized
// name is a construction-time error.
func New(name string, cfg Config) (plandecision.Resolver, error) {
	switch name {
	case NameAutoAllowUnsafe:
		return autoallow.New(autoallow.Config{
			AcknowledgeUnsafeAutoAllow: cfg.AcknowledgeUnsafeAutoAllow,
			Logger:                     cfg.Logger,
			Telemetry:                  cfg.Telemetry,
		})
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownDriver, name)
	}
}
