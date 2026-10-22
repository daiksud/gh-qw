---
type: adr
title: "ADR-0005: Use a dedicated worktree root and `worktree` subcommand"
description: "Place linked worktrees in a deterministic root and manage them through one command group."
resource: gh-qw
tags: [gh-qw, adr, adr-0005, git, worktree, filesystem]
timestamp: 2026-08-05
---

# ADR-0005: Use a dedicated worktree root and `worktree` subcommand

- **Status:** Accepted
- **Date:** 2026-08-05
- **Updated:** 2026-10-22
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0004](../0004-ghq-directory-convention/), [ADR-0006](../0006-command-set-v1/), [ADR-0007](../0007-configuration-sources/)

## Context

Linked worktrees need stable, discoverable locations without cluttering the visible roots that
hold main clones. Allowing every operation to choose an arbitrary path would weaken the
path-as-identity model and make listing, cleanup, and navigation less predictable.

Worktree operations also form a distinct lifecycle that should not be scattered across unrelated
top-level commands.

## Decision

We will place worktrees created by `gh-qw` under a dedicated worktree root, defaulting to
`$XDG_DATA_HOME/ghqw/worktrees` when `XDG_DATA_HOME` is absolute and otherwise
`~/.local/share/ghqw/worktrees`, using
`<worktree-root>/<host>/<owner>/<repo>/<branch>`. Slashes in branch names form nested
directories.

The `gh qw worktree` command group will own the supported add, list, remove, and prune operations.
It will derive destination paths rather than accept arbitrary destinations, while Git remains the
authority for refs and worktree relationships. The deterministic slot remains stable after a
branch switch or detached checkout; it is not a second ref database.

Branch creation, tracking, remote guessing, `checkout.defaultRemote`, ambiguity, and ref
resolution are delegated to native Git. gh-qw does not prefer `origin`, parse human-readable Git
failures to infer absence, or reproduce fetch-refspec matching. Explicit named creation/reset
uses `git branch` followed by `git worktree add`; if the second operation fails, the branch is
preserved and the partial result is reported rather than silently rolled back.

Automatic mode without a start point passes the requested branch to Git and does not fall back
to a network lookup. Only explicit `-b`/`-B` without a start point asks `gh api` for the GitHub
default branch and, when its local ref is missing, invokes `gh repo sync` after destination
validation and re-observes the ref. The [CLI reference](../../../reference/cli/#worktree-add)
records this breaking migration: use `-b <branch>` for GitHub-default creation or supply a start
point, and use Git configuration to disambiguate remotes.

## Consequences

### Positive

- Main repository roots remain focused on durable clones.
- A repository and branch determine a worktree's path without additional metadata.
- Related lifecycle operations share one discoverable command group.
- Git owns remote/ref semantics, including custom fetch mappings, without a competing gh-qw implementation.

### Negative

- Branch names such as `feat` and `feat/x` can collide as filesystem paths and must be rejected.
- Users needing arbitrary worktree placement must use Git directly, outside the `gh-qw` layout.
- Scripts relying on the former implicit default-branch fallback or origin-first preference must migrate.

### Neutral

- The dedicated root is configurable, but every `gh-qw`-created worktree follows the same shape.
- Path/identity validation, confirmation, output and partial-failure policy remain gh-qw responsibilities.
