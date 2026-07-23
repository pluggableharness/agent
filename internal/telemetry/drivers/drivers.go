// Package drivers is the driver selector for internal/telemetry
// (go-layout.md's driver pattern): the sole place that switches on a
// backend name. Adding a new exporter backend means adding a new
// sub-package here plus one line in New's switch — nothing else in the
// kernel should ever branch on a driver name.
package drivers

import (
	"fmt"

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/otlpgrpc"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/otlphttp"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/stdout"
)

// ErrUnknownDriver is returned by New for a name outside the known set.
var ErrUnknownDriver = fmt.Errorf("telemetry: drivers: unknown driver")

// New returns the telemetry.Backend named by name, configured from cfg.
// The recognized names are "otlpgrpc", "otlphttp", "stdout", "noop", and
// "fake" — matching telemetry.Config.Backend's documented values.
func New(name string, cfg telemetry.Config) (telemetry.Backend, error) {
	switch name {
	case "otlpgrpc":
		return otlpgrpc.New(cfg), nil
	case "otlphttp":
		return otlphttp.New(cfg), nil
	case "stdout":
		return stdout.New(), nil
	case "noop":
		return noop.New(), nil
	case "fake":
		return fake.New(), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownDriver, name)
	}
}
