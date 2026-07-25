# internal/modelrequest — agent notes

- **Pure domain, no exceptions.** This package MUST NOT import `log/slog`
  or `internal/telemetry` — it is the `.claude/rules/logging-telemetry.md`
  exemption category (I/O-free, deterministic, single goroutine, ~90%+
  covered). The caller logs `FellBackThinking`/`FellBackToolChoice` and any
  `ValidateContent` rejection around this package; nothing in here does.
- **`ValidateParams` always clones, never mutates the caller's `req`.**
  `Resolved` is `proto.Clone(req)`, then mutated in place on the clone.
  Don't "optimize" this into mutating `req` directly — callers may hold
  onto the original `*modelv1.GenerationParams` (e.g. to retry against a
  fallback model in a routing chain) and must see it unchanged.
- **`TOOL_CHOICE_MODE_AUTO` is always valid, regardless of
  `ModelSpec.supported_tool_choice_modes`.** `data-types.md#generationparams`
  defines `AUTO` as "equivalent to omitting `tool_choice` entirely" — it's
  already the fallback target, so checking it against the declared list
  would be checking a value against itself. `toolChoiceSupported` in
  `params.go` special-cases this before the list-membership check; don't
  remove the special case to "simplify" it into a single `slices.Contains`
  call, or a model declaring an empty `supported_tool_choice_modes` would
  incorrectly reject an explicit `AUTO` request.
- **Thinking validation is mode-gated, not just range-gated.** A
  `thinking_budget_tokens` value can sit numerically inside some
  `ThinkingBudgetRange` and still be invalid, if the resolved model's
  `ThinkingSpec.mode` isn't `THINKING_MODE_CONTINUOUS_BUDGET` (same for
  `thinking_effort` against `THINKING_MODE_DISCRETE_EFFORT`). Check the
  mode first in `budgetInRange`/the effort branch of `ValidateParams`,
  not just the numeric/list membership — `TestValidateParamsThinkingEffort`'s
  "mode mismatch" case is the regression guard for this.
- **`ValidateContent` returns the *first* violation, in message-then-block
  order.** It does not collect every unsupported block in one request —
  matching the brief's "returns `ErrUnsupportedContent` naming the first
  unsupported block kind and its position." Don't change this to a
  multi-error collection without re-reading the task brief/spec quotes
  this package cites; nothing in `data-types.md` asks for that, and it
  would change the wire-visible error shape callers already depend on.
- **`PlaceCacheBreakpoints` only computes `after_assembled_context`, never
  `after_tools`.** This is a deliberate scope limit tied to the function's
  fixed signature (`sections`, `messages`, `spec` — no `[]ToolDeclaration`,
  no prior-turn state), not an oversight or a TODO. If a future task adds
  a tools parameter or turn history to this function, re-derive the
  `after_tools` case from `protocol.md#cache-breakpoint-placement-policy`'s
  "breakpoint after `after_tools` when the tool declaration list is stable
  turn to turn" language at that point — don't guess at a stability signal
  that doesn't exist in the current inputs.
- **The stable-prefix check only looks at `sections[0]`.** The wire
  `CacheBreakpoint` shape has no per-section marker inside
  `assembled_context`, only a marker for the chain as a whole — so "is
  there a static prefix worth naming" reduces to "is the very first
  section static," since that is the only condition under which the
  single available marker (`after_assembled_context`) actually buys a
  vendor a cache hit on anything. A trailing `STABILITY_DYNAMIC` section
  after a static lead is fine (see
  `TestPlaceCacheBreakpointsTrailingDynamicSectionStillMarksWholeChain`) —
  don't tighten this into "every section must be static," which isn't
  what the spec's worked example or ordering rationale asks for.
