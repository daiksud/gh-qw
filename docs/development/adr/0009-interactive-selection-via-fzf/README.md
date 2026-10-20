---
type: adr
title: "ADR-0009: Leave interactive selection to callers"
description: "Keep list focused on identities and paths, with selection and navigation owned by the caller."
resource: gh-qw
tags: [gh-qw, adr, adr-0009, cli, list]
timestamp: 2026-10-20
---

# ADR-0009: Leave interactive selection to callers

- **Status:** Accepted
- **Date:** 2026-08-06
- **Updated:** 2026-10-20
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0006](../0006-command-set-v1/)

## Context

`gh qw list` prints canonical identities or paths for discovered repositories and, with
`--worktree`, their linked worktrees. The former `--fzf` integration delegated selection to an
external executable but still required candidate mapping, selection handling, a dedicated runner,
and cancellation-status handling in gh-qw. That unused integration adds maintenance without
serving the project's current requirements.

[ADR-0006](../0006-command-set-v1/) leaves navigation to the caller's own shell. Removing the
picker integration keeps list focused on producing output that callers can compose themselves.

## Decision

Remove `--fzf` and its implementation without adding a compatibility flag or replacement picker.
Ordinary list filtering, sorting, exact matching, full paths, shortest unique suffixes, and
linked-worktree output retain their existing contracts. `gh-qw` neither selects an entry nor
launches a shell.

The removed flag is an unknown-flag usage error with exit status `2`. The former cancellation
status `130` is no longer part of gh-qw's public contract. Existing shell functions must remove
`--fzf`; callers can use `list --full-path` as input to their own selection and navigation logic.
See the [CLI migration guidance](../../../reference/cli/#migrating-shell-functions).

This accepted ADR is revised in place under the project's `0.y.z` policy. Its original URL stays
stable for existing links.

## Consequences

- gh-qw no longer discovers or invokes a picker, maps selections back to paths, or handles picker
  cancellation.
- Users of the former flag must update their shell functions; this is a breaking CLI change.
- Selection tools and their exit statuses belong to the caller's composition rather than gh-qw's
  public API.
