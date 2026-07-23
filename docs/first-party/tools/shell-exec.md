# Shell / Bash Execution

## What it is

Shell execution lets the model run an arbitrary command in a real shell (or shell-like PTY) on the host or sandbox the harness controls, and get back stdout/stderr, an exit code, or an ongoing interactive session. It is the closest thing a coding agent has to a general-purpose escape hatch: every other tool operation (file edit, search, git, build, test, lint, package management) can in principle be reimplemented as "run the right command."

In a coding agent's turn loop, shell execution is typically the tool that closes the loop between "I wrote code" and "I know whether it works" — it runs builds, executes test suites, invokes linters, and inspects the repository (`git status`, `ls`, `cat`) when a more specific tool isn't available or convenient. It is also the single highest-variance capability from a risk standpoint: unlike `file_edit` or `grep`, whose blast radius is bounded by the operation's own semantics, a shell tool's blast radius is bounded only by what the underlying shell can do — anything the host user can do.

The capability spans a spectrum of implementation sophistication: from a single blocking `run(cmd) -> (stdout, stderr, exit_code)` call, through session-based PTY execution supporting interactive programs (`psql`, `vim`, REPLs), to fully deferred/staged execution where commands are collected and only run at an explicit later "apply" step.

## Design considerations

**One-shot vs. session/PTY duality.** A single blocking call structurally cannot support an interactive program that needs input after launch (a REPL, `vim`, `gdb`). Designs that want to support this expose a second, session-oriented primitive — a call that returns a session handle, plus a way to write further input into it — alongside the simple one-shot call, rather than forcing both patterns into one signature.

**Deferred/staged execution as a distinct pattern.** Some designs don't execute a command the moment the model calls the tool at all — they accumulate proposed commands and run them only when a human explicitly triggers a later "apply" step. That moves the permission gate from "per call" to "the apply step itself," at the cost of the model never seeing intermediate results before committing to the whole batch.

**Read-only carve-outs are a policy concern, not a protocol one.** Prompting for every `ls` makes a shell tool unusable, so a common pattern is to allowlist a small set of known-safe, read-only commands (`ls`, `pwd`, `git status`) that bypass approval, while everything else stays gated. Some designs push this further and let the model itself declare a call safe (a per-call "safe to auto-run" boolean); that is inherently weaker than a harness-enforced check, since a self-declared safety signal is only as trustworthy as the model's own judgment — a documented failure mode elsewhere in this space is a URL-fetch tool with no equivalent safety flag at all, which allowed maliciously-crafted page content to reach the network unattended.

## Permission, sandbox & risk classification

Shell/bash execution is classified `kind = resource`, `risk = high` uniformly — the same operation covers both `ls` and `rm -rf`, rather than sniffing the command string to reclassify individual invocations. That per-invocation reclassification is a policy concern (an allowlist or classifier in `agent.hcl`), not something the tool protocol's `GetSchema` should attempt. A provider that wants to expose guaranteed-safe, freely-executing shell access MAY additionally declare a narrower, genuinely read-only operation (e.g. `shell_read`, restricted to a fixed allowed command set) as `data_source`, but the general `bash` operation stays `resource`/`high`. See [`reference-catalog.md`](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls) for the full reasoning.

Shell execution is also the most common target of OS-level sandboxing (bubblewrap, Seatbelt, Landlock) among tool operations — restricting writes to the working directory, blocking outbound network by default, or isolating the whole process tree — precisely because its blast radius is otherwise unbounded. Network egress from shell-initiated commands is a persistent, under-controlled edge case: many filesystem-sandboxed setups still allow arbitrary outbound HTTP from a shelled-out command, so network access via `exec` should be treated as no more trustworthy than the sandbox's weakest boundary.

Whether a reference `exec` provider should expose the PTY/session pattern as a distinct operation pair (e.g. `exec_start` returning a session handle, plus `exec_write_stdin`) alongside a single-shot `bash` is an open design question this protocol has not yet settled.
