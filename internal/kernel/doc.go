// Package kernel is the composition root: the one place every other
// internal/ package is constructed, wired to its collaborators, and torn
// down again.
//
// Run is the whole surface. It brings the process up in dependency order
// (XDG paths, telemetry, configuration, logging, the state backend and
// event bus, the plugin supervisor, the provider catalog, then the turn
// and session drivers), runs exactly one non-interactive session with the
// caller's prompt, prints that session's final message, and shuts every
// phase back down in reverse — including when bring-up itself failed
// partway through.
//
// Nothing above this package exists except cmd/agent, which parses flags
// and maps Run's error to a process exit code (.claude/rules/go-layout.md:
// cmd/ stays thin). Nothing below it knows this package exists.
//
// # Scope of this build
//
// This is a root-sessions-only kernel. There is no frontend attach path,
// so there is no interactive REPL, no RunSession sub-agent spawning, and
// the two operator-approved tracked deviations that stand in for a missing
// frontend are wired here: internal/plandecision/drivers/autoallow for
// ask-decision plan items and internal/interactive/drivers/unattended for
// interactive-kind tool calls. Both are constructed in newTurnStack, where
// the acknowledgment and the WARN that goes with it are deliberately
// impossible to miss.
//
// See docs/specifications/architecture.md#cli-shape for the CLI this
// eventually becomes, and this package's CLAUDE.md for the wiring order,
// the late-binding seam the turn stack needs, and every default value
// chosen here rather than mandated by a specification.
package kernel
