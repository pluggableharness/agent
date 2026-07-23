package telemetry

import "errors"

// ErrNilBackend is returned by New when backend is nil.
var ErrNilBackend = errors.New("telemetry: nil backend")

// ErrInvalidConfig is returned by Config.Validate (and therefore by New,
// which validates cfg before doing anything else) when a field's value is
// individually malformed.
var ErrInvalidConfig = errors.New("telemetry: invalid config")
