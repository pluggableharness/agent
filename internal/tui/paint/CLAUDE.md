# internal/tui/paint — agent notes

## The `default:` branch in `Painter.Node` is load-bearing

It handles a `RenderNode` variant added to the enum after this build shipped, and it delegates to `pkg/frontend.FallbackText`. The protocol states as a MUST that a frontend render such a node gracefully rather than erroring or dropping it. Do not replace this with a panic, an error return, or an empty string, and do not reimplement the traversal locally — `pkg/frontend` already owns it.

## `Targets` must mirror the painter's traversal exactly

`walk.go` and `paint.go` walk the same tree with the same path scheme (`parent + "." + index`, rooted at the caller-supplied path). If they diverge, the action cursor highlights one node and activates another — a bug that no compiler catches. Change them together, and keep `TargetsAt`/`TreeAt` root paths in sync.

Collapsed children are deliberately excluded from `Targets`: a cursor must not move over content the operator cannot see.

## `GroupNode` adds no chrome, on purpose

The spec defines it as a transparent container. A test asserts that a grouped pair of nodes renders byte-identically to the same nodes rendered separately and joined. Adding a border or indent "for readability" breaks the node's stated meaning.

## Assertions must strip ANSI

Lip Gloss emits SGR escapes per character for some styles (underline, notably), so `strings.Contains(got, "text")` fails on styled output. The tests use a `plain()` helper; use it for any new assertion on rendered content.

## This package is pure domain

No `log/slog`, no `internal/telemetry`, no I/O — the pure-domain exemption in `.claude/rules/logging-telemetry.md` applies.
