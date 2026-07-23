# internal/policy — agent notes

- **`Conflicts` implements a correction, not the literal spec text.**
  `configuration.md` §7.2's original wording ("identical specificity tuple
  ⇒ config-load-time error") has false positives — see the "Correction
  (found during internal/policy implementation)" paragraph added to that
  section. `Conflicts` requires an identical tuple **and** every
  field both rules specify having an equal value. Don't simplify this back
  to a bare tuple-equality check.
- **`Match.Kind` is a 2-value type (`KindResource`/`KindDataSource`), not a
  reuse of `toolv1.ToolKind`'s 3 values.** This is a documented, deliberate
  v1 limitation (`configuration.md` §7.1's `PolicyMatch.kind` never widened
  to include `interactive`), not a bug — see `types.go`'s doc comment on
  `Kind` before "fixing" it by adding a `KindInteractive` value (that would
  require a `configuration.md` spec change first, not just a code change).
- **`Evaluate` returns 3 values**: `(action, matchedRule, downgraded)` —
  `downgraded` is `true` only when a winning `ActionAsk` got flipped to
  `ActionDeny` for a `data_source`/`interactive` call. This is an
  intentional extension beyond `configuration.md`'s literal pseudocode
  (which only returns an action), added so a caller can log the downgrade
  as §7.3 asks the kernel to.
- **The `interactive`-kind extension is a documented extrapolation**, not
  something `configuration.md` states directly: both the empty-candidates
  default and the ask→deny downgrade apply to `TOOL_KIND_INTERACTIVE`
  calls, justified by `agent-loop.md` §5.4bis's "reuses this section's
  precheck verbatim." If that section ever changes, revisit `evaluate.go`.
- **`ValidateRules` returns on the first conflict found**, not every
  conflicting pair — deliberate, documented in its doc comment.
