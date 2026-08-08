---
type: adr
title: "ADR-0018: Integrate worktree add/remove and rm with Herdr workspaces"
description: "Let --herdr open and close a Herdr workspace for a linked worktree by delegating to the external herdr executable."
resource: gh-qw
tags: [gh-qw, adr, adr-0018, cli, herdr, worktree]
timestamp: 2026-08-07
---

# ADR-0018: Integrate worktree add/remove and rm with Herdr workspaces

- **Status:** Accepted
- **Date:** 2026-08-07
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0006](../0006-command-set-v1/), [ADR-0009](../0009-interactive-selection-via-fzf/)

## Context

`gh qw worktree add` creates a linked worktree at its deterministic branch path
([ADR-0004](../0004-ghq-directory-convention/), [ADR-0005](../0005-dedicated-worktree-root/)) and prints
only that absolute path; `worktree remove` and `rm` remove one back out.
[ADR-0006](../0006-command-set-v1/) already decided that `gh-qw` never launches a shell of its own —
it outputs paths and leaves navigation to the caller.

Herdr is a separate terminal-workspace manager for coding agents, organizing terminals into
workspaces, tabs, and panes and exposing them through its own `herdr` executable and socket API. A
person or agent driving several linked worktrees through Herdr today has to manually run
`herdr workspace create --cwd <path> --focus` after every `worktree add` and manually close the
matching workspace after every `worktree remove`/`rm`. That manual step is repetitive, easy to
forget, and leaves stale workspaces open at paths that no longer exist once a worktree is removed.

[ADR-0009](../0009-interactive-selection-via-fzf/) already established the shape for exactly this
kind of integration: delegate interaction to an external tool through its own documented CLI,
report only the outcome, and do not reopen the shell-launch boundary from ADR-0006. Adding a Herdr
integration should follow that same shape rather than inventing a new one.

## Decision

`worktree add`, `worktree remove`, and `rm` (for an `@branch` linked-worktree target only) gain a
`--herdr`/`--no-herdr` flag pair. A new `herdr` boolean key in `config.toml` and a `GHQW_HERDR`
environment variable let a person enable the integration by default instead of passing the flag on
every invocation. These resolve with one fixed precedence: an explicit flag, then `GHQW_HERDR`,
then the configuration key, then disabled.

When enabled, `worktree add` runs `herdr workspace create --cwd <new-worktree-path>
--label <repo>@<branch> --focus` through the `herdr` executable resolved from `PATH`, after the
worktree itself is created and validated and after gh-qw's own `stdout` contract — the new absolute
path — is already satisfied. `worktree remove` and `rm` resolve the workspace `herdr` already has
open for that worktree path with `herdr worktree list` **before** removing anything (Herdr's own
worktree listing depends on Git's own registration, which the removal itself would erase), remove
the worktree exactly as they do today, and only then close the resolved workspace with
`herdr workspace close`. A worktree with no open workspace is left alone: that is an ordinary,
expected outcome, not a failure.

An explicit `--herdr` outside of a Herdr-managed pane (`HERDR_ENV` unset or not `1`) is a usage
error, since the person explicitly asked for an integration that cannot exist there. Implicit
enablement through `GHQW_HERDR` or `config.toml` outside Herdr instead only warns on `stderr` and
skips the integration, leaving the command's own result and exit status unaffected, so a shared
configuration file does not break ordinary use on a machine or in CI that never runs inside Herdr.

Exactly like `list --fzf`, `herdr`'s own JSON responses are parsed internally by a small
`internal/herdr` package and never appear on gh-qw's own `stdout`; gh-qw still never launches a
shell of its own, deferring entirely to Herdr's own workspace and pane management.

## Consequences

### Positive

- A person or agent driving several linked worktrees through Herdr gets one workspace per worktree
  automatically, without a manual `herdr workspace create`/`cd` step and without a stale workspace
  surviving `worktree remove`/`rm`.
- The integration is one small, focused `internal/herdr` package that talks to a single documented
  external CLI, matching the shape already proven by `internal/fzf` and `internal/ghcmd` — no new
  architectural precedent is required.
- Because the resolved enablement is decided before any Git operation starts and never changes
  `worktree add`/`remove`'s own deterministic path or `stdout` contract, Herdr integration is
  strictly additive to the existing CLI reference.

### Negative

- `--herdr` requires a separately installed `herdr` on `PATH` and a running Herdr session; outside
  one, the explicit flag is a hard usage error rather than silently doing nothing.
- Implicit (`GHQW_HERDR`/`config.toml`) enablement is deliberately lenient (warn and skip), so a
  person relying on it outside Herdr might not immediately notice a workspace was never created if
  they miss the warning.

### Neutral

- `worktree prune` and starting an agent inside the new workspace are intentionally out of scope
  for this decision. A future ADR can extend the same `internal/herdr` seam if that need arises.
