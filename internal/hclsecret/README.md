# internal/hclsecret

The one secret-handling mechanism shared, verbatim, by every place
`agent.hcl` (and the global config file) can carry a credential:
`specifications/configuration.md` §4's rule that a `sensitive` attribute's
expression MUST be exactly a call to the built-in `env(name)` function — no
string interpolation, no concatenation, no default-fallback wrapping —
checked against the raw, unevaluated expression syntax, before anything is
evaluated.

## What this package does

- `env.go` — the `env(name)` HCL function itself, registered into an
  `hcl.EvalContext` wherever a body might contain a sensitive expression.
  Fails fast (returns an error) if the named variable is unset, rather than
  silently resolving to an empty string.
- `validate.go` — `ValidateSensitiveExpr`, which inspects an attribute's
  *unevaluated* expression AST and rejects anything that isn't exactly
  `env("NAME")` with a literal string argument.

## How it fits in

Two call sites use this identically: a `provider{}` block's `sensitive`
attributes (`internal/config`) and `registry_mirror.mirror.auth`
(`internal/registry`). Both packages import this one rather than each
re-implementing the check.
