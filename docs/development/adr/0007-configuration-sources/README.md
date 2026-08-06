---
type: adr
title: "ADR-0007: Configure gh-qw through its file and environment variables"
description: "Use XDG configuration, data, and cache locations with explicit GHQW_* precedence."
resource: gh-qw
tags: [gh-qw, adr, adr-0007, configuration, environment]
timestamp: 2026-08-05
---

# ADR-0007: Configure gh-qw through its file and environment variables

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0002](../0002-distribute-as-gh-cli-extension/), [ADR-0004](../0004-ghq-directory-convention/), [ADR-0005](../0005-dedicated-worktree-root/), [ADR-0006](../0006-command-set-v1/)

## Context

Repository and worktree roots need explicit, predictable configuration across interactive shells,
automation, and operating systems. Reading both application configuration and Git configuration
would create overlapping precedence rules and make behavior depend on unrelated global state.

Migration is different from normal operation: it must be able to discover where an existing ghq
installation stores repositories without adopting ghq's settings as ongoing `gh-qw`
configuration.

## Decision

Normal `gh-qw` configuration comes only from its configuration file and `GHQW_*` environment
variables. XDG variables are used only when absolute; unset, empty, or relative values use the
home-relative location:

| Purpose | XDG location | Home-relative location |
| --- | --- | --- |
| Configuration | `$XDG_CONFIG_HOME/ghqw/config.toml` | `~/.config/ghqw/config.toml` |
| Worktree data | `$XDG_DATA_HOME/ghqw/worktrees` | `~/.local/share/ghqw/worktrees` |
| Account cache | `$XDG_CACHE_HOME/ghqw/accounts.json` | `~/.cache/ghqw/accounts.json` |

Repository roots resolve independently as non-empty `GHQW_ROOT`, then `root` in the configuration
file, then `~/ghqw`. The worktree root resolves as non-empty `GHQW_WORKTREE_ROOT`, then
`worktree_root` in the configuration file, then the XDG data location. The account cache has no
user-configurable path beyond `XDG_CACHE_HOME`.

The configuration and cache schemas are strict. Invalid configuration is a usage error. Account
cache read, parse, validation, and write failures stop account resolution before network access.
Detailed keys, validation, and path normalization belong in the configuration reference.

`gh-qw` will not read Git configuration for normal behavior. The `migrate` command may inspect
legacy ghq source-discovery settings, including the `ghq.root` Git configuration, solely to find
repositories to migrate. Those values are not imported, persisted, or consulted after source
discovery.

## Consequences

### Positive

- Configuration ownership and precedence are explicit.
- Environment overrides support CI and temporary invocation without changing persistent state.
- Normal behavior cannot silently change because of ghq Git configuration.
- User-authored configuration, worktree data, and machine-written account choices occupy the
  corresponding XDG categories.

### Negative

- Existing ghq settings do not automatically configure `gh-qw`.
- Users who want non-default roots must set them again in a `gh-qw` source.
- Invalid account cache state blocks network operations until corrected or removed.

### Neutral

- Migration reads legacy configuration as input data, not as product configuration.
