package kernel

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/doomloop"
)

// shutdown tears every phase down in reverse construction order and
// returns every failure, joined.
//
// Two properties are load-bearing, and both mirror
// internal/pluginhost.Supervisor.Shutdown's own precedent:
//
//   - A failure in one phase MUST NOT abort the rest. A wedged plugin must
//     not be able to prevent the telemetry pipeline from flushing or the
//     event bus from closing.
//   - The whole teardown runs on a fresh, bounded context derived from
//     context.WithoutCancel(ctx). Shutdown is normally reached *because*
//     ctx was canceled (a SIGINT, or a bring-up failure on a canceled
//     context), and a drain-then-kill sequence on an already-Done context
//     drains nothing.
//
// shutdown is safe to call on a partially-built kernel: every phase is
// skipped when its field is nil, which is exactly what bringUp leaves
// behind when it fails partway.
func (k *kernel) shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	var errs []error
	phase := func(name string, fn func() error) {
		if fn == nil {
			return
		}
		if err := fn(); err != nil {
			// Logged AND collected, deliberately: the joined error is
			// what Run returns, but a teardown failure that happens
			// alongside a session failure would otherwise be invisible
			// in the log an operator is actually reading.
			k.logger.ErrorContext(ctx, "kernel: shutdown phase failed", "phase", name, "error", err)
			errs = append(errs, fmt.Errorf("kernel: shutdown %s: %w", name, err))
		}
	}

	// Plugins first: they are the only phase that can still be calling
	// back into the event bus, the log server, and the state backend.
	// Supervisor.Shutdown already tears its own set down in reverse
	// launch order.
	if k.supervisor != nil {
		phase("plugins", func() error { return k.supervisor.Shutdown(ctx) })
	}
	// Then the relay, which the plugin callback servers were uploading
	// through.
	if k.relay != nil {
		phase("telemetry relay", func() error { return k.relay.Stop(ctx) })
	}
	// Then telemetry itself, so the spans and metrics everything above
	// just produced get one last flush.
	if k.telem != nil {
		phase("telemetry", func() error { return k.telem.Shutdown(ctx) })
	}
	// The bootstrap Provider is normally already gone by here; it survives
	// only when bring-up failed between constructing it and replacing it.
	if k.bootTelem != nil {
		phase("bootstrap telemetry", func() error { return k.bootTelem.Shutdown(ctx) })
	}
	// The bus last: it is what everything above published onto.
	if k.bus != nil {
		phase("event bus", func() error { return k.bus.Close() })
	}

	return errors.Join(errs...)
}

// maxDepth resolves settings.max_depth into internal/session's
// KernelDefaultMaxDepth. A nil (unset) value means "effectively
// unbounded", which that package expresses as math.MaxInt32 — reused here
// rather than picking a second, disagreeing number for one idea.
func maxDepth(setting *int) int {
	if setting == nil || *setting <= 0 {
		return math.MaxInt32
	}
	return *setting
}

// doomLoopConfig translates settings.doom_loop{} into the shape
// internal/doomloop takes. config.LoadFile already applies
// DefaultDoomLoopSettings for an absent block; the zero-value guard covers
// a hand-built Settings that never went through it.
func doomLoopConfig(s config.DoomLoopSettings) doomloop.Config {
	if s.WindowSize <= 0 || s.Threshold <= 0 {
		return doomloop.DefaultConfig
	}
	return doomloop.Config{WindowSize: s.WindowSize, Threshold: s.Threshold}
}
