# internal/config — agent notes

- **Provider block bodies are deferred, not decoded at `LoadFile` time.**
  `Config.ProviderBodies[name]` is a raw `hcl.Body`. Don't try to eagerly
  decode it in `provider.go` — there's no `ConfigSchema` available yet at
  that point, since the schema comes from the plugin's own
  `GetCapabilities`/`GetSchema` response, which requires the plugin to
  already be running. A test that expects `LoadFile` alone to catch a
  malformed *provider-specific* attribute (e.g. a `sensitive` field with a
  literal secret) is testing the wrong function — that's
  `DecodeProviderConfig`'s job (see `bridge_test.go`).
- **`Settings.Retry` and `Settings.Observability` always get their
  `Default*` values applied upfront**, in both `load.go`'s `decode()`
  (before any block is processed) and `decodeSettings` itself (when the
  `settings{}` block is present but its nested `retry{}`/`observability{}`
  sub-block is absent). Both paths need the defaults applied
  independently — a config with no `settings{}` block at all never calls
  `decodeSettings`. `Observability` contains a `map[string]string`
  (`ResourceAttrs`), so it's not comparable with `!=` — tests use
  `reflect.DeepEqual` against it, unlike `RetrySettings`.
- **`observability{}`'s fields are all-or-nothing, like `retry{}`** — every
  attribute is `Required: true` in `observabilitySchema` except
  `resource_attrs` (genuinely optional). Adding a new field to
  `Observability` means adding it to `observabilitySchema`,
  `decodeObservability`, `DefaultObservability`, **and every existing test
  fixture in `load_test.go`/`settings_test.go` that declares an
  `observability{}` block** — a fixture missing the new required field
  fails (or, worse, silently tests the wrong thing: a wrong-type test case
  in a table like `TestLoadFile_settingsWrongTypes` will still "pass" by
  erroring on the missing-field check instead of the intended type check,
  masking what it claims to verify). This happened when `logs_enabled` was
  added — check every fixture, not just the ones the compiler complains
  about.
- **`defaultSettings()` (`settings.go`) is the single source of the
  "settings{} absent entirely" values**, called by both `load.go`'s
  `decode()` and `decodeSettings`. Adding a new defaulted `Settings` field
  means adding it there once, not in two places — the earlier two-copy
  arrangement for `Retry`/`Observability` is what made the previous bullet's
  drift hazard real. `Settings.MaxDepth` is the one deliberate exception:
  it stays `nil` in `defaultSettings()`, see its own bullet below.
- **`event_bus{}` and `doom_loop{}` deliberately do NOT follow
  `retry{}`/`observability{}`'s all-or-nothing convention**, for two
  different reasons, both recorded in their schema vars' doc comments.
  `event_bus{}` declares exactly one attribute
  (`blocks-reference.md#event_bus`), so no partial-specification case
  exists to guard against — an empty `event_bus {}` block and an absent one
  are indistinguishable by design, both yielding `DefaultEventBus`
  (`subscribe_queue_bound = 1024`). `doom_loop{}` has two attributes, but
  `turn-algorithm.md#doom-loop-detection` states a MUST-level default for
  `window_size` and `threshold` *individually*, which is incompatible with
  requiring both together — each falls back to `DefaultDoomLoopSettings`
  on its own. Don't "fix" either by adding `Required: true`.
- **`DefaultDoomLoopSettings` reads its numbers from
  `doomloop.DefaultConfig`, never restates them.** There is exactly one
  source of truth for the window/threshold defaults, and
  `TestDecodeSettings_defaultDoomLoopMatchesDoomloopPackage` locks that in.
  Range validation (`threshold` in [3, 5], `window_size >= threshold`) is
  `doomloop.New`'s job, not this package's — `internal/config` carries what
  was declared.
- **`Settings.DefaultHookTimeoutMS` (5000) and `Settings.DefaultToolTimeoutMS`
  (30000) are project-level judgment calls, not spec-mandated values.**
  `hook-dispatch.md#per-subscriber-timeout` and `tool/protocol.md#getschema`
  each establish that a kernel-configurable default exists without ever
  naming one. Both are exported consts so the "what is the number, and who
  chose it" answer lives in one place; if the spec later states a canonical
  value, change the const and the doc comment together.
- **`Settings.MaxDepth` is a `*int` that this package never defaults.**
  `nil` (unset) and an explicit `0` ("this root session may spawn nothing")
  are genuinely different declarations, mirroring
  `agentprofile.AgentProfile.MaxDepth`'s shape. Unlike
  `Retry`/`Observability`/`EventBus`/`DoomLoop`, `nil` is carried through
  untouched: `agentprofile.RootRemainingDepth` already resolves the unset
  case via its own `kernelDefault` parameter, so defaulting it here too
  would be a second source of truth that could disagree with the first.
  Resolve `nil` at the call site.
- **`Hook.TimeoutMS` is `*int` for the same nil-vs-zero reason** — `nil`
  means "fall back to `Settings.DefaultHookTimeoutMS`", `0` means the
  subscriber declared a zero-millisecond deadline. `timeout_ms` is the only
  optional attribute in `hookSchema`.
- **`TelemetryConfig` (`telemetry.go`) is the only bridge from this
  package's HCL types into `internal/telemetry.Config`** — that package
  stays HCL/cty-free (its own `CLAUDE.md`), so the translation lives here,
  not there. Three asymmetries are intentional and documented on the
  function: `Observability.Protocol` is consumed into `Config.Backend`
  (`grpc` → `otlpgrpc`, `http` → `otlphttp`, anything else → `""` so
  `drivers.New` rejects it loudly rather than guessing a transport);
  `Config.Insecure`/`Config.ServiceVersion` have no `observability{}`
  attribute to read from and stay at their zero values until
  `blocks-reference.md#observability` grows one; and `Settings.LogLevel`
  isn't carried because `telemetry.Config` has no log-severity field.
  `settings.telemetry = false` forces `Backend: "noop"` regardless of
  `observability{}`'s contents, per
  `settings-and-global.md#the-telemetry-switch`.
- **HCL single-line block syntax only permits one argument.**
  `primary { provider = "x", id = "y" }` is a parse error — has to be
  written as a multi-line block. This bit the test fixtures more than once
  during development; if a fixture in this package's tests suddenly fails
  to parse, check this first before assuming the decoder is broken.
- **The `sensitive`/`env(...)` enforcement in `bridge.go`
  (`validateSensitiveAttrs`) runs on the raw, unevaluated body BEFORE
  `hcldec.Decode`** — calling `body.JustAttributes()` ahead of
  `hcldec.Decode(body, ...)` on the same `hcl.Body` is intentional and
  safe (HCL bodies support being read multiple different ways), not a bug
  to "simplify" into one pass.
- **`configuration.md` §7.2 was corrected** (see the "Correction (found
  during internal/policy implementation)" paragraph) — `policy.go`'s
  `parsePolicyKind` only accepts the spec's literal 2-value
  `resource`/`data_source` subset, not `tool.md`'s 3-value `ToolKind`; this
  is intentional, see `internal/policy`'s own `CLAUDE.md`.
- **`LoadFile` is instrumented per `internal/CLAUDE.md`'s file-I/O
  mandate.** Its signature is
  `LoadFile(ctx context.Context, prov *telemetry.Provider, path string) (*Config, error)`
  — `ctx` first, then `prov`, per `go-style.md`. The body opens
  `prov.StartConfigLoad(ctx, path)`, ends it via `telemetry.EndSpan` in a
  `defer` closing over a named `err` return (every return path must assign
  to that named `err`, or the deferred `EndSpan` silently records a false
  "success" on a failed load — this is the one easy way to get this
  instrumentation subtly wrong), and logs one `slog.DebugContext` entry
  carrying only `path`. No `ERROR` log is added inside `LoadFile` itself —
  it returns its errors, and `go-style.md`'s "a function returns an error
  or logs it, never both" applies to this package like any other.
- **`decode` and `DecodeProviderConfig` (`bridge.go`) are deliberately
  left uninstrumented — don't "complete the pass" by adding telemetry to
  them.** Both are in-memory only (no file I/O, no process/network
  boundary), so neither trips `internal/CLAUDE.md`'s "performs I/O"
  trigger. `DecodeProviderConfig` specifically evaluates `env(...)` via
  `hclsecret.EnvFunction` internally, which is the same secret-handling
  reason `internal/hclsecret`'s own `CLAUDE.md` gives for leaving that
  package's `EnvFunction`/`ValidateSensitiveExpr` uninstrumented — see that
  file for the full reasoning, not repeated here.
