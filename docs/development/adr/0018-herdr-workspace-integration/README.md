---
type: adr
title: "ADR-0018: Leave workspace management to callers"
description: "Keep Git worktree operations independent of terminal workspace management."
resource: gh-qw
tags: [gh-qw, adr, adr-0018, cli, worktree]
timestamp: 2026-10-21
---

# ADR-0018: Leave workspace management to callers

- **Status:** Accepted
- **Date:** 2026-08-07
- **Updated:** 2026-10-21
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0006](../0006-command-set-v1/)

## Context

`worktree add` creates a linked worktree at its deterministic branch path and prints that path.
`worktree remove` and `rm` remove managed worktrees after checking their registration and physical
boundaries. Terminal workspace management is separate from these Git operations.

The former Herdr integration coupled successful Git operations to workspace creation, lookup,
and closing. Its flags, configuration, environment precedence, process execution, and error
aggregation expanded the command surface without contributing to repository or worktree management.

## Decision

Remove the Herdr integration completely. `worktree add`, `worktree remove` (including `--gone`),
and `rm` no longer create, find, or close terminal workspaces. Remove the integration package,
command helpers, and their dedicated tests. Preserve worktree layout, output, confirmation,
registration checks, and physical boundary checks.

The former `--herdr` and `--no-herdr` flags are unknown flags with CLI status `2`. The former
`herdr` configuration key is an unknown key with status `2`, including when set to `false`.
`GHQW_HERDR` and `HERDR_ENV` are ignored. There is no replacement workspace integration or setting.
Callers manage their terminal workspaces separately.

This breaking change requires removing the retired flags and configuration key from existing
scripts and settings. See the [CLI migration guidance](../../../reference/cli/#migrating-herdr-integrations)
and [configuration reference](../../../reference/configuration/#retired-workspace-settings).
The ADR is revised in place during `0.y.z` development; its existing URL remains stable.

## Consequences

### Positive

- Git worktree completion and exit status no longer depend on a terminal workspace manager.
- Commands have fewer flags and dependencies, and configuration has fewer precedence rules.
- Existing removal safety checks remain independently testable without workspace side effects.

### Negative

- Existing users must remove retired flags and the configuration key before upgrading.
- Callers that want terminal workspaces must manage them outside `gh-qw`.
