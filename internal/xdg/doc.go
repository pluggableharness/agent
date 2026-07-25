// Package xdg resolves the kernel's XDG Base Directory layout into concrete
// paths. See docs/specifications/architecture.md#xdg-layout for the
// authoritative specification.
//
// This is a pure-domain package: it is I/O-free (except for resolving $HOME
// via os.UserHomeDir when needed), deterministic, and single-threaded.
// It MUST NOT import log/slog or internal/telemetry, per
// .claude/rules/logging-telemetry.md's exemption for pure-domain packages.
// Call sites are responsible for logging and instrumentation around the
// Resolve function's result.
//
// Paths are computed once at kernel startup via Resolve(projectDir) and
// used throughout the kernel's lifetime. Resolve does not create any
// directories — each consumer is responsible for creating its own paths
// with appropriate permissions (e.g. internal/statebackend creates
// StateDir/sessions with 0700).
package xdg
