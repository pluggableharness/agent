# internal/providerresolve

Turns a loaded `agent.hcl`, the project lock file, and the operator's global
config into a deterministically ordered list of launchable plugin binaries.

This is the step between "configuration has been parsed" and "a plugin
subprocess can be spawned". Every `Resolved` entry it returns names a concrete
on-disk binary that exists and is executable, and — unless it came from a
`dev_overrides` entry — has a lock-file row with a recorded checksum for this
platform. Actually launching what it returns is
[`internal/pluginhost`](../pluginhost/README.md)'s job; this package resolves
and never downloads, installs, launches, or configures anything.

## The two exported operations

### `Order`

Sorts `required_providers` local names by the textual position of each name's
`provider{}` block, read from `config.Config.ProviderRanges`.

[`configuration/agent-profiles.md`](../../docs/specifications/configuration/agent-profiles.md)
resolves hook ordering by "textual declaration position in `agent.hcl`", with
an implicit subscription's position being wherever its `provider{}` block
appears. That rule is applied here, one stage earlier, because this same
sequence becomes launch order — and launch order is hook-dispatch order, whose
reverse is shutdown order. Deriving all three from one textual sort keeps them
consistent by construction rather than by three separate implementations
agreeing.

A local name with no `provider{}` block (declared in `required_providers` but
never configured) has no textual position, so it sorts after every name that
does, then by name. The result is a total order in every case, never Go map
iteration order — see [`determinism.md`](../../.claude/rules/determinism.md).

### `Resolve`

Walks `Order`'s sequence and resolves each name:

| Path | Lock row | Cached binary | Checksum | Executable |
|---|---|---|---|---|
| `dev_overrides` match | bypassed | bypassed (path is given) | bypassed | required |
| everything else | required | required | required for this platform | required |

`dev_overrides` winning first is
[`settings-and-global.md#dev_overrides`](../../docs/specifications/configuration/settings-and-global.md):
"the kernel MUST use that binary directly instead of resolving through the
registry/version-constraint machinery."

Failures accumulate. `Resolve` never stops at the first unresolvable provider;
it returns one `*MissingError` carrying every problem, in `Order`'s sequence,
so a fresh checkout learns everything it has to install in a single pass —
the same "report what's missing before touching anything" posture
[`architecture.md#state-backend`](../../docs/specifications/architecture.md#state-backend)
describes for a session's producer set.

## What it deliberately does not decide

`Resolved.Category` comes only from the lock file's cached record
(`registry.LockedProvider.Category`) and is `CATEGORY_UNSPECIFIED` whenever
that record is absent — always for a `dev_overrides` provider, and for any row
written before the field existed.
[`blocks-reference.md#required_providers`](../../docs/specifications/configuration/blocks-reference.md)
is explicit that a provider's category "is never declared here — the kernel
discovers it after loading the plugin", so an unspecified category here is a
correct answer, not a gap: `internal/pluginhost` probes for it with a live
`Describe`.
