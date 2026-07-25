// Package providerresolve turns a loaded agent.hcl's required_providers
// block, the project lock file, and the operator's global config into a
// deterministically ordered list of launchable plugin binaries.
//
// It is the step between "configuration has been parsed" and "a plugin
// subprocess can be spawned": every entry it returns names a concrete
// on-disk binary that exists, is executable, and — unless it came from a
// dev_overrides entry — has a lock-file row with a recorded checksum for
// this platform.
//
// Two properties are load-bearing:
//
//   - Ordering is total and textual. Order sorts local names by the
//     source position of their provider{} block
//     (docs/specifications/configuration/agent-profiles.md's
//     declaration-order rule), never by map iteration order
//     (.claude/rules/determinism.md). That single sequence later drives
//     launch order, which is hook-dispatch order, which reversed is
//     shutdown order.
//
//   - Failure is accumulated, not fail-fast. Resolve reports every
//     unresolvable provider in one *MissingError rather than stopping at
//     the first, so a fresh checkout learns everything it has to install
//     in a single pass — the same "report what's missing before touching
//     anything" posture docs/specifications/architecture.md#state-backend
//     describes for a session's producer set.
//
// This package resolves; it never downloads, installs, launches, or
// configures anything. Launching what it returns is
// internal/pluginhost's job.
package providerresolve
