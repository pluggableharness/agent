// Package plugin implements providercatalog.Catalog over a live
// *pluginhost.Registry — the real, non-fake driver
// internal/providercatalog/CLAUDE.md and internal/pluginhost/CLAUDE.md
// both anticipated ("Live.Capabilities is what makes that driver
// possible later"). This is that driver: a future composition root
// (internal/kernel) builds a Registry via pluginhost.Supervisor.Start,
// hands it to plugin.New, and gets back a Catalog to give to
// internal/turn, internal/session, internal/hookdispatch, and
// internal/contextassembly.
//
// This package is read-only in the same sense providercatalog.Catalog
// itself is: it never launches, configures, dials, or shuts down a
// plugin. pluginhost.Supervisor already owns that lifecycle in full;
// this package only translates already-live pluginhost.Live values —
// each one already launched, Described, checksum-verified, Configured,
// and registered — into providercatalog's resolved-handle shapes.
//
// # Eager vs. lazy extraction
//
// New extracts every ModelSpec, ToolSchema, ContextCapabilities, and
// hook subscription out of the registry's Live values once, at
// construction time, rather than re-deriving them from Live.Capabilities
// on every Model/Tool/Contexts/Hook call. This is safe for the reason
// internal/pluginhost/CLAUDE.md's own note anticipates: a Registry is
// only ever mutated by Supervisor.Start, in one goroutine, and that call
// completes in full before a Catalog is ever constructed over it (New's
// own doc comment on this point, and the sibling internal/kernel
// composition root this drives, both hold this ordering as an
// invariant). There is no "the registry changed after New returned" case
// to design around in v1 — required_providers has no reload/hot-add
// mechanism (configuration/blocks-reference.md#required_providers).
//
// Eager extraction buys two things a lazy design would not: repeated
// type assertions against Live.Capabilities (one per category, needed
// on every call otherwise) happen exactly once per provider, and —
// materially more important — the one genuinely expensive operation
// this package performs, resolving ToolHandle.SupportsPreview (below),
// happens once at startup rather than smeared unpredictably across a
// turn's first tool dispatch for each operation. A turn-loop caller
// should never pay a network round trip inside what looks like a pure
// map lookup.
//
// Each accessor still returns a value the caller cannot use to mutate
// Catalog's internal state — a fresh map for ModelSpecs/ToolNames, a
// fresh slice for Contexts — matching drivers/fake's own copy-safety
// contract exactly, so the two drivers are interchangeable from a
// caller's point of view.
//
// # Resolving SupportsPreview
//
// docs/specifications/tool/protocol.md#preview makes Preview a MAY, and
// confirmed by reading pkg/tool/proto/v1: no ToolSchema field, no
// GetSchemaResponse field, nothing in the wire protocol at all signals
// whether a given operation implements it. The only way to find out is
// to ask — call it and see what comes back. This package resolves
// SupportsPreview by doing exactly that, once per tool operation, during
// New:
//
//  1. Call Preview with a synthetic ToolCall carrying the real operation
//     name, an empty arguments Struct, and a probe-only call ID — never
//     a real call the plan/apply gate is about to make.
//  2. If the RPC returns codes.Unimplemented, SupportsPreview is false.
//  3. Any other outcome — success, or any other error code, including
//     one caused by the synthetic arguments failing that operation's own
//     input-schema validation — means SupportsPreview is true.
//
// Step 3 is the load-bearing part: reading pkg/tool/server.go's own
// Preview handler confirms the Unimplemented check
// (s.impl.(Previewer)) happens strictly before the wrapped Provider's
// Preview method — and therefore before any argument validation — ever
// runs. A plugin that does implement Previewer for this operation can
// therefore never answer Unimplemented, regardless of whether this
// package's synthetic empty-arguments probe would itself pass that
// operation's declared input schema. This mirrors
// internal/tokencount.Counter's own resolution of the sibling optional
// RPC, CountTokens, which also treats codes.Unimplemented as the one
// reliable signal (status.Code(err), never string matching) — the
// deliberate difference is that tokencount resolves lazily, memoizing on
// a Count call that happens anyway during normal turn operation, while
// Preview has no such naturally-occurring call to piggyback on: nothing
// invokes it except the plan/apply gate building a preview, which needs
// the answer already in hand before ever attempting the call. Eager,
// catalog-build-time resolution is therefore the only way to avoid the
// exact "discover Unimplemented mid-turn" outcome
// providercatalog.ToolHandle.SupportsPreview's own doc comment says this
// field exists to prevent.
//
// A probe RPC is bounded by a short per-call timeout (previewProbeTimeout)
// derived from New's ctx, so one unresponsive plugin cannot hang catalog
// construction. A timeout or a canceled probe resolves conservatively to
// SupportsPreview=false and is logged at WARN — false is always the safe
// default because protocol.md#preview separately requires every caller to
// tolerate Preview's absence at call time regardless of what this field
// says, so under-reporting support only costs a documented raw-arguments
// fallback, never a broken plan/apply gate.
//
// This trust model relies on protocol.md#preview's own guarantee that
// Preview "MUST NOT mutate anything and MUST be side-effect-free... for
// Preview itself regardless of the underlying call's ToolKind" — the
// same trust boundary the kernel already extends to every other RPC a
// plugin answers.
//
// # Position and LaunchIndex
//
// providercatalog.ContextHandle.Position is documented as "this
// provider's declaration order in agent.hcl". Confirmed by reading both
// packages directly rather than assuming it:
// pluginhost.Supervisor.Start launches s.cfg.Resolved in order,
// stamping each Live.LaunchIndex with that loop's index
// (supervisor.go); s.cfg.Resolved is built from
// providerresolve.Order's own required_providers-declaration-order
// output. pluginhost.Registry.ByCategory returns its matches in launch
// order (registry.go's own doc comment, exercised by
// TestRegistry_orderingAndFiltering). LaunchIndex therefore already *is*
// agent.hcl declaration order, and Registry.ByCategory(CATEGORY_CONTEXT)
// already returns context providers in that order — so this package
// sets ContextHandle.Position directly from Live.LaunchIndex rather than
// renumbering 0, 1, 2, ... within just the context-category subset.
// Either scheme satisfies internal/contextassembly.Assemble, which only
// ever compares Position values against each other
// (cmp.Compare(x.Position, y.Position) after cloning and re-sorting its
// own input) and never assumes a gapless 0-based sequence; LaunchIndex
// is preferred here because it is already computed and because it lets
// Position double as a stable identifier across every category, not just
// within context providers.
//
// # A known gap: ContextHandle.TokenBudget never reflects an agent.hcl
// override
//
// providercatalog.ContextHandle.TokenBudget is documented as "the
// agent.hcl override if one was declared, otherwise
// Capabilities.DefaultTokenBudget" — configuration/blocks-reference.md's
// reserved token_budget convention field, decoded as part of that
// provider's own provider{} block body. Confirmed by reading
// internal/pluginhost end to end: the decoded config
// (*structpb.Struct, from config.DecodeProviderConfig) is used exactly
// once, inside Supervisor.startOne, to call Configure and to install
// into that plugin's kernelcallback.Server slot for its own GetConfig
// callback — it is never retained on pluginhost.Live or exposed by
// pluginhost.Registry in any other form. There is therefore no path from
// a *pluginhost.Registry alone to a loaded context provider's own
// token_budget override; this package always sets TokenBudget to
// Capabilities.DefaultTokenBudget, unconditionally. Closing this gap
// needs pluginhost to retain (or newly expose) each Live's own decoded
// config — out of scope for this package, and flagged here rather than
// silently guessed at.
package plugin
