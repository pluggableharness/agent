# internal/agentprofile — agent notes

- **`RootRemainingDepth` and `ChildRemainingDepth` are two separate
  functions on purpose**, not one generic function with a "is this the
  root" flag — `configuration.md` §8.4 presents them as two distinct
  equations, and collapsing them would obscure that a root session's
  fallback is `kernelDefault` while a child's is `+inf`
  (`unboundedDepth = math.MaxInt`), a real semantic difference.
- **`ResolveTools`'s handling of an unknown concrete tool name is a
  deliberate choice, not an oversight**: a `"<provider>.<tool_name>"` entry
  naming a *loaded* provider but a tool name that provider doesn't actually
  advertise returns `ErrUnknownTool` (config-validation territory, "typo in
  agent.hcl silently grants nothing" is worse than a load-time error). A
  wildcard or concrete entry naming a provider **not loaded at all** is a
  no-op, not an error — the provider may simply not be present this
  session. Don't unify these two cases without re-reading why they differ.
- **Malformed tool-scoping strings** (no `.`, or an empty provider/tool
  half, e.g. `"filesystem"` or `"filesystem."`) return `ErrMalformedToolScope`
  rather than being silently parsed as a provider or tool with an empty
  name.
- **Model eligibility (`satisfies` in `model.go`) uses the protobuf
  `Get*()` accessors**, not direct field access, so it stays nil-safe if a
  caller's `specs` map ever holds a `nil *ModelSpec` — keep using the
  accessors if this function is extended.
- **This package has no dependency on `internal/hclsecret` or
  `internal/policy`** — keep it that way; it's a leaf.
