# Local dev run

Scaffolding for running the kernel against **locally built plugin binaries** instead of published releases. Nothing here is part of the build; it exists so the frontend-hosted path can actually be exercised end to end.

## How it resolves

Two files, two different roles:

| File | Role | Found via |
|---|---|---|
| `agent/config.hcl` | Global config — carries `dev_overrides` | `$XDG_CONFIG_HOME/agent/config.hcl` |
| `agent.hcl` | Project config — `required_providers`, settings, profile | `-config` |

The global config is normally per-user at `~/.config/agent/config.hcl`, and its path is not a flag — `internal/xdg.Resolve` computes it. But it reads `XDG_CONFIG_HOME`, so pointing that at this directory makes the whole run repo-local without any code change.

`dev_overrides` is the mechanism that matters: a name listed there resolves straight to a binary on disk, skipping registry resolution, the lock file, and checksum verification entirely. Identity comes from the plugin's own `Describe` RPC, which is what `dev_overrides` exists for.

## Run it

Build the plugins first, then:

```sh
XDG_CONFIG_HOME=$PWD/.dev \
XDG_STATE_HOME=$PWD/.dev/state \
XDG_CACHE_HOME=$PWD/.dev/cache \
XDG_DATA_HOME=$PWD/.dev/data \
  ./bin/agent -config .dev/agent.hcl 2>.dev/agent.log
```

All four XDG vars are redirected so a dev run writes no sessions, caches, or data into your real home.

**No `-prompt`.** That is what selects frontend-hosted mode: the kernel brings every provider up, installs the frontend host, and waits while the frontend drives sessions over the callback channel. It exits when the frontend's subprocess does — quitting the TUI ends the kernel — or on Ctrl-C.

With `-prompt` you get the old behavior instead: one non-interactive session, final message on stdout, exit. Useful for checking a model provider without involving a frontend.

## `2>.dev/agent.log` is not optional

The frontend owns the terminal — it opens `/dev/tty` directly, precisely because under go-plugin stdin/stdout belong to the handshake. The kernel logs to stderr. Point both at the same terminal and kernel log lines paint straight over the UI. Redirect stderr, then `tail -f .dev/agent.log` in a second terminal.

## Before it will work

Edit `agent/config.hcl` — the paths there are absolute and machine-specific — and set a real model id in `agent.hcl`.

A session cannot start without a model provider: `internal/session` resolves the profile's model chain against the live catalog and fails with `ErrNoDefaultModel` when nothing answers. A frontend alone brings the kernel up and gives you a UI, but the first submitted prompt will fail.
