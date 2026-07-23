// Package hclsecret implements the one secret-handling mechanism shared,
// verbatim, by every place agent.hcl (and the global config file) can carry
// a credential: specifications/configuration.md §4's rule that a
// `sensitive` attribute's expression MUST be exactly a call to the built-in
// `env(name)` function — no string interpolation, no concatenation, no
// default-fallback wrapping — checked against the RAW, unevaluated
// expression syntax, before anything is evaluated.
//
// Two call sites use this package identically: a provider{} block's
// sensitive attributes (configuration.md §4/§6) and
// registry_mirror.mirror.auth (configuration.md §10).
package hclsecret
