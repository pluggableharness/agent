# Glob / File Search

## 1. What it is

Glob/file search is path-based file discovery: given a pattern (typically shell-glob syntax, e.g. `**/*.ts`), return the list of matching file paths in the workspace, without reading or searching file *contents*. It answers "what files exist matching this shape" as distinct from grep/content search, which answers "which files contain this text" — sibling but separate common-core capabilities.

In a coding agent's workflow this is typically the first move in an explore-before-edit sequence: the model globs for candidate files (by extension, directory, or name fragment), then reads or greps the matches. It is cheap, deterministic, and treated as categorically lower-risk than content search or shell execution, because it only enumerates the filesystem's shape rather than exposing its contents.

Several harnesses fold this capability into a broader file-discovery tool (name-plus-fuzzy-match) rather than a pure glob matcher; a few skip a dedicated tool entirely and rely on the shell (`find`, `ls`) inside a general-purpose exec tool instead.

## 2. Adoption and mechanism

Glob/file search is one of the more widely adopted capabilities in this space, though not fully universal — a small number of harnesses have no dedicated glob tool and rely on shell-based discovery instead, consistent with those harnesses' broader design of treating "act on the filesystem" as a batch operation rather than an interactive discovery step.

No naming convention has emerged: `glob`, `file_search`, `find_by_name` (wrapping `fd`), `find_path`, and other names are all in active use for closely related operations — one of several capabilities where naming-convention wars remain unresolved.

**Result capping and ordering.** A near-consistent pattern among implementations that publish a limit: results capped at a fixed count (often around 50–100), sorted by modification time. Implementations framed as "disambiguate one file's location" rather than "bulk-enumerate matching files" tend to cap much lower (single digits to a dozen). Some add limit/offset pagination instead of a hard cap, letting the model page through large result sets.

**Ignore-file interaction diverges.** Some implementations deliberately do not respect `.gitignore` by default even when their sibling content-search tool does — a notable asymmetry within the same harness. Others filter by `.gitignore` (and sometimes a harness-specific ignore file) as standard behavior, with some additionally hard-excluding build/cache/secrets directories regardless of ignore-file configuration.

**Adjacent/composite tools.** Several implementations extend the base capability rather than keeping it a pure path-matcher: combining glob matching with a batch content read in one call; extending matching to external or cloned repositories rather than just the local workspace; wrapping search in a sub-agent that chains glob, grep, and read to answer conceptual queries the literal glob tool can't. Some tool descriptions are dynamically augmented with a hint to prefer a semantic-search alternative when that subsystem is active — an instance of a broader "model/feature-gated tool routing" pattern applied to search tool selection.

## 3. Permission, sandbox & safety

Glob/file search is near-uniformly classified as the *lowest*-risk tool category — a read-only enumeration of paths, no content exposure, no side effects. It typically sits in the same no-approval tier as file read and content search.

The occasional outlier requires approval by default and supports allow/deny path scoping — a stricter default than most peers, but this generally reflects that harness's broader conservative posture (a capability-based, deny-wins permission system with hardcoded always-prompt rules for sensitive paths) rather than something specific to glob's own risk profile.

No implementation describes OS-level sandboxing scoped specifically to glob/file search — sandboxing is uniformly discussed in the context of shell/exec tools, not path enumeration. Where a harness does sandbox filesystem access at all, that sandbox's write restrictions are irrelevant to a tool that only reads path metadata.

## 4. Convergent patterns & divergences

**Convergent**: broad presence, read-only classification with no-approval-required as the default gate wherever the harness has a tiered permission model at all, and a widely shared implementation detail (mtime-sorted results with a numeric cap, typically 50–100) among the implementations that document their limits.

**Divergent**: naming has no dominant convention; result-cap size varies by an order of magnitude, reflecting different framing — "disambiguate one file's location" vs. "bulk-enumerate matching files"; and ignore-file handling is inconsistent even within a single harness that clearly respects ignore files for its content-search tool but not for its glob tool.

**Observed trend**: the tool is increasingly treated as an entry point to richer discovery rather than a standalone primitive — several implementations layer semantic or cross-repository search options adjacent to or wrapping the literal glob operation, suggesting the field is moving toward glob-as-a-fallback beneath a smarter concept-level search, mirroring a similar grep-to-semantic-search evolution in content search.

## 5. Implications for PluggableHarness Agent

Glob/file search is part of PluggableHarness Agent's first-party tool reference catalog: provider `search`, operation `glob`, `kind: data_source`, `risk: read_only` (see [the reference catalog](../../specifications/tool/reference-catalog.md)). The cross-tool evidence above supports the classification without complication: every implementation that documents a permission tier for this operation places it at the free/no-approval end, which is exactly what `data_source`/`read_only` is designed to signal. A stricter default in one outlier implementation reads as a harness-wide conservative policy choice rather than evidence that glob itself carries elevated risk — consistent with `kind`/`risk` describing the *operation's* intrinsic blast radius, with per-operator tightening left to `agent.hcl` policy.

One point worth surfacing for whoever finalizes the reference `glob` provider's `input_schema`: real implementations diverge on `.gitignore` handling — some deliberately exclude it from glob while including it in grep; others include it in glob itself. The reference catalog's current note for `glob` ("Pattern-based path matching") doesn't take a position on this, and it's the kind of behavioral detail that affects reproducibility across a plugin ecosystem — worth an explicit MUST/SHOULD when the reference provider's schema is written, rather than leaving it to convention.
