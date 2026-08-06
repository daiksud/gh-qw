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
authority for refs and worktree relationships.

## Consequences

### Positive

- Main repository roots remain focused on durable clones.
- A repository and branch determine a worktree's path without additional metadata.
- Related lifecycle operations share one discoverable command group.

### Negative

- Branch names such as `feat` and `feat/x` can collide as filesystem paths and must be rejected.
- Users needing arbitrary worktree placement must use Git directly, outside the `gh-qw` layout.

### Neutral

- The dedicated root is configurable, but every `gh-qw`-created worktree follows the same shape.
