# Git / VCS

## 1. What it is

"git/vcs" denotes any tool-level capability that operates on a project's version-control state: reading diffs or history, staging/committing changes, branching or worktree management, or querying a remote host's VCS metadata (commits, PRs) via API. In a coding agent's workflow this sits adjacent to file editing — after the model changes files, something needs to decide whether/when those changes become a commit, and the agent may want to read git history or diffs as context before or during editing.

Unlike file read/write/edit, this is not a capability coding harnesses have converged on as a discrete, well-specified tool. Signal is thin and scattered across several unrelated things that all happen to touch "git": worktree lifecycle management, read-only diff display, remote GitHub API access (commits/PRs, not local git), and — in rare cases — a genuine first-class git integration layer.

## 2. Adoption and mechanism

This is a thin capability overall, and the partial implementations that exist are not one pattern wearing different names — they're at least four unrelated things:

- **Worktree lifecycle, not general git**: a tool pair that creates or switches into an isolated git worktree for branch-isolated work, typically paired with subagent fan-out. This exposes none of git's other primitives (status, diff, log, commit) as callable tools.
- **Read-only diff viewers**: a parameter-less tool that shows the working-tree diff vs. the last commit, surfacing diff/history information as context rather than as a git operation the model can chain (stage, commit, branch).
- **Remote GitHub API, mistaken for local git**: tools that list/search/get commits via a hosted GitHub API (often via an MCP server), not the local `.git` repository. These answer "what happened in this repo's history on GitHub" rather than "what's my working-tree diff right now."
- **The genuine outlier — a first-class git integration.** At least one harness auto-commits every accepted set of LLM-produced edits via its own internal git-repository class (generating the commit message with a separate, cheaper model), exposes a raw git passthrough command, a manual commit command, and an undo command scoped to revert only its own last commit (it will not undo a manually-made commit). That harness's repo-map context injection (a ranked map of git-tracked files' function/class signatures) also uses the repository as an implicit memory substrate — a "memory via git history" pattern. None of this is model-invoked function-calling, though — it's the harness's own code acting automatically after each edit, plus user-typed commands; the model itself cannot call commit or git directly.

No implementation exposes a model-callable, JSON-schema `git_status`, `git_diff`, `git_commit`, or `git_log` tool of the kind file_edit or shell_exec have as settled reference patterns. Where structured git access exists at all, it's typically a single narrow read.

## 3. Permission, sandbox & safety

Because there's no common dedicated git tool, there's no common permission treatment either — each fragment inherits the permission tier of whatever category it actually belongs to:

- **Worktree creation**: often treated as low-risk (it doesn't touch the primary working tree) and slots into the harness's read-tier auto-approval rather than its write/exec approval tier.
- **Diff viewing**: typically the lowest permission tier, alongside file read/list/grep, because it's read-only and instant.
- **Remote commit search**: governed by the harness's general MCP approval path rather than a git-specific rule — often restricted to read-only toolsets by default, or requiring explicit user approval before first execution like any other MCP server.
- **General git-via-shell** (the majority pattern): wherever git is actually used — `git status`, `git diff`, `git commit` — it typically goes through the harness's general shell/exec tool and inherits that tool's approval and sandbox posture rather than any git-specific gate. This matters because shell-level git access includes destructive operations (`git reset --hard`, `git push --force`, history rewrites) with no harness-level awareness that "this shell command happens to be git." One partial exception: some harnesses' OS-level terminal sandbox blocks writes to `.git` by default — a blanket filesystem-path protection, not git-semantic reasoning about what the command does.
- **Automatic-commit models** carry their own risk profile: because a harness that commits after *every* accepted edit does so without a per-commit approval prompt, its safety net is git itself (reversibility via undo and ordinary git history) rather than a pre-commit gate.

No implementation treats "this is a git-mutating operation" as a distinct risk category the way `bash`/`exec` is treated as uniformly `high` risk (see [the reference catalog](../../specifications/tool/reference-catalog.md)) — git mutations are either invisible (folded into generic shell risk) or, in the auto-commit case, deliberately unthrottled because git's own history is the safety mechanism.

## 4. Convergent patterns & divergences

There is effectively no convergence to report. Where file_edit or shell_exec show the field settling (or visibly fighting over) a small set of named patterns, git/vcs shows the opposite: most harnesses have no dedicated git tool at all, and the ones that touch git each hit on a different, mostly tangential thing (worktree helper, diff viewer, remote API wrapper, sandbox rule). The one point of near-agreement is negative: git mutations that do happen are almost universally routed through the general shell tool rather than a dedicated wrapper, which is consistent with how thin most harnesses' explicit git awareness is.

The one real divergence worth naming is the auto-commit approach versus everyone else's: treating the git repository as *the* state and memory substrate (auto-commit, repo-map from git-tracked files, undo as the correction mechanism) is a design that predates and sits outside the native-function-calling paradigm most other harnesses converged on. No later harness has widely adopted the auto-commit-per-edit model; the field's growing emphasis on OS-level sandboxing and per-call approval went a different direction — toward gating *execution*, not toward treating VCS commits as the recovery mechanism.

## 5. Implications for PluggableHarness Agent

git/vcs does not appear in the first-party tool reference catalog at all — not as a shipped first-party operation, and not even as one of the explicitly-named differentiators left to third-party providers (browser automation, cron/scheduling, and memory-as-a-tool are each named there; git/vcs is not). That omission holds up well: git/vcs coverage is thin and scattered rather than convergent, and thinner than the capabilities the reference catalog does call out by name as differentiators.

Given that, this doesn't support adding a dedicated `git` operation to the reference catalog, nor does it identify a strong enough differentiator pattern to name git/vcs explicitly as a differentiator category either. The practical takeaway is closer to what most implementations already do: git access is adequately served by the existing `exec`/`bash` reference operation (`kind: resource`, `risk: high`) — a third-party or operator-authored `agent.hcl` policy could scope a narrower `git status`/`git diff`-only allowlist through the same read-only data-source carve-out already suggested for `bash` generally (a provider MAY additionally expose a narrower read-only `data_source` operation restricted to a small allowed command set — see [the reference catalog](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls)). That carve-out, not a new git-specific operation, is the PluggableHarness Agent-relevant pattern here.

If a third-party provider did want to build a dedicated git tool (say, to match an auto-commit/undo model or a read-only diff viewer), it would follow the same `resource`/`data_source` split the reference catalog already uses elsewhere: a `git_diff`/`git_log`/`git_status` operation as `data_source`/`read_only`, and a `git_commit`/`git_checkout`-style mutation as `resource` with a `risk` of at least `moderate` — but nothing here obligates PluggableHarness Agent to ship that as part of its reference set.
