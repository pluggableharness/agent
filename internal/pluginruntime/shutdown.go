package pluginruntime

import (
	"context"
	"time"
)

// defaultDrainTimeout is the hardcoded window Close gives go-plugin's own
// Kill() to finish gracefully before escalating to a hard subprocess-tree
// teardown, unless the caller's ctx already carries a tighter deadline.
// Operator decision #3 (plan "Operator decisions (locked)"): a fixed
// package-level constant, not a Config field.
const defaultDrainTimeout = 5 * time.Second

// Close performs a graceful shutdown of p's plugin subprocess: it calls
// the underlying go-plugin Client's Kill() — never a bare SIGKILL as the
// first move (plugin-runtime.md) — and gives it up to the drain window
// (ctx's own deadline if tighter, else defaultDrainTimeout) to finish. If
// Kill() has not returned by then, Close escalates by canceling the
// launch context Launch derived internally, which forces
// exec.CommandContext's own subprocess-tree teardown; this is the "hard
// kill" path, reached only as a timeout escalation, never as the default.
//
// Close always waits for the underlying Kill()/subprocess teardown to
// actually finish before returning, so it never leaks the goroutine
// racing against the drain window.
func (p *Plugin) Close(ctx context.Context) error {
	return closeWithKill(ctx, p.client.Kill, p.cancelLaunch, defaultDrainTimeout)
}

// closeWithKill implements Close's drain-then-escalate logic against
// killFn and cancelLaunch directly, rather than a concrete *plugin.Client,
// so the timing behavior is unit-testable without a real subprocess.
func closeWithKill(ctx context.Context, killFn func(), cancelLaunch context.CancelFunc, drainTimeout time.Duration) error {
	deadline := time.Now().Add(drainTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	done := make(chan struct{})
	go func() {
		killFn()
		close(done)
	}()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		if cancelLaunch != nil {
			cancelLaunch()
		}
		<-done // Kill()/the canceled subprocess still finishes asynchronously; wait rather than leak the goroutine.
		return nil
	}
}
