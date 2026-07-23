# internal/hclsecret — agent notes

- **Syntax validation and fail-fast-if-unset are deliberately split across
  two files/mechanisms.** `ValidateSensitiveExpr` (`validate.go`) only
  checks the expression's *shape*, before anything is evaluated.
  `EnvFunction` (`env.go`) enforces "fails fast if the variable is unset,"
  but only fires when the expression is actually *evaluated* (via
  `hcldec.Decode` or similar, elsewhere). Don't try to make
  `ValidateSensitiveExpr` also check whether the variable is set — that
  would require evaluating the expression, defeating the point of
  validating it pre-evaluation.
- **The literal-string check unwraps `hclsyntax.TemplateExpr`, not
  `hclsyntax.LiteralValueExpr` directly.** In native HCL syntax, a plain
  quoted string like `"FOO"` parses as a `TemplateExpr` containing exactly
  one `LiteralValueExpr` part — checking for a bare `LiteralValueExpr`
  argument would incorrectly reject every ordinary string literal. See
  `stringLiteralValue` in `validate.go` if this needs touching again.
- **The argument to `env(...)` is required to be a literal string**, not
  just any expression — a conservative reading of "exactly `env(name)`"
  from `configuration.md` §4. Don't loosen this to accept a variable
  reference or another function call as the argument without re-reading
  that section; it would reopen exactly the kind of expression complexity
  the spec wants to forbid.
- **This package is deliberately exempt from `internal/CLAUDE.md`'s
  logging/telemetry instrumentation mandate — don't "fix" this during an
  enforcement pass.** `EnvFunction` (`env.go`) does call `os.LookupEnv`,
  but `internal/CLAUDE.md`'s I/O enumeration (network / subprocess / file
  / sqlite / `hashicorp/go-plugin` RPC) doesn't list process-environment
  lookups, so it doesn't trip the "performs I/O" trigger on a strict
  reading. More importantly, `EnvFunction`'s entire job is resolving
  SECRET values (`env("API_KEY")` etc.) — `internal/CLAUDE.md`'s own
  "Never log secrets" rule, which extends to every level including
  `TRACE`, argues *against* instrumenting this function: an entry/exit
  log here is one careless future edit away from interpolating the
  resolved value into a log attribute. `ValidateSensitiveExpr` similarly
  does no real I/O (a pure AST/syntax check on an unevaluated
  `hcl.Expression`) and isn't a candidate either. This pairs with the
  same call already made for `internal/config.DecodeProviderConfig`,
  which also evaluates `env()` internally and is deliberately left
  uninstrumented for the same secret-handling reason.
