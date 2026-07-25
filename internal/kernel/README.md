# internal/kernel

The composition root. Every other `internal/` package is constructed here, wired to its collaborators, and torn down here — and nothing else in the tree does any of that.

`Run(ctx, Options) error` is the whole surface. [`cmd/agent`](../../cmd/agent) parses flags, calls it once, and maps its error to a process exit code; that is all `cmd/` is allowed to do ([`go-layout.md`](../../.claude/rules/go-layout.md)).

## What this build is

A **root-sessions-only, non-interactive** kernel. `Run` loads `agent.hcl`, launches every resolved provider plugin, runs exactly one session with `Options.Prompt`, prints that session's final message to `Options.Stdout`, and shuts down.

There is no frontend plugin category yet, and that shapes three things:

- **No interactive mode.** [`architecture.md#cli-shape`](../../docs/specifications/architecture.md#cli-shape) describes a single interactive `agent` command; that arrives with the frontend category. A prompt is required because there is nowhere to ask for one.
- **No sub-agent spawning.** `RunSession` is still `codes.Unimplemented` in `internal/kernelcallback`, so one process runs one root session. The turn stack refuses a second session id rather than serving it.
- **Two tracked deviations are wired**, both loudly: [`internal/plandecision/drivers/autoallow`](../plandecision/drivers/autoallow) auto-approves every `ask`-decision plan item, and [`internal/interactive/drivers/unattended`](../interactive/drivers/unattended) refuses every interactive-kind call. See [`CLAUDE.md`](CLAUDE.md) for where the acknowledgment lives and why the two differ in direction.

There is also no download or install path for a provider binary. `providerresolve` resolves through `dev_overrides` or an existing lock file plus a cached binary, and anything it cannot resolve becomes one startup error naming every missing entry — never a silent hang.

## Bring-up order

`bringUp` constructs in dependency order; `shutdown` reverses it. The sequence, and what each step needs from the one before it:

| # | Step | Why here |
|---|---|---|
| 1 | `xdg.Resolve(workingDirectory)` | every later path comes from it |
| 2 | bootstrap telemetry (disabled, `noop` backend) | `config.LoadFile` requires a `*telemetry.Provider`, and the real one's configuration is inside the file being loaded |
| 3 | `config.LoadFile` | everything below is configured by it |
| 4 | real telemetry via `config.TelemetryConfig`, then the bootstrap Provider is shut down | the `settings.telemetry` switch lives in `config`, not here |
| 5 | logging: build the handler, `slog.SetDefault` once | every package below falls back to `slog.Default()` |
| 6 | global config + lock file, both tolerating absence | inputs to provider resolution |
| 7 | state backend, event bus, telemetry relay, log server | the process-wide singletons every plugin's callback server shares |
| 8 | scope/session/plugin registries, token counter | `pluginhost.Config` needs all four |
| 9 | `providerresolve.Resolve` | needs config + lock + global + cache dir |
| 10 | `pluginhost.NewSupervisor` + `Start` | launches every plugin subprocess |
| 11 | `providercatalog/drivers/plugin.New` | reads the now-populated registry |
| 12 | hook registry + dispatcher | resolves subscribers through the catalog |

Everything below that is **per session**, not per process, and is built lazily on the first turn — see [`CLAUDE.md`](CLAUDE.md), which explains the ordering problem that forces it.

## Shutdown

`shutdown` runs plugins → telemetry relay → telemetry → event bus, on a fresh bounded context derived from `context.WithoutCancel(ctx)` (teardown is normally reached *because* the caller's context was canceled). A failure in one phase never aborts the rest; every failure is logged and joined into the returned error. It is safe on a partially-built kernel, which is exactly what a bring-up failure leaves behind.
