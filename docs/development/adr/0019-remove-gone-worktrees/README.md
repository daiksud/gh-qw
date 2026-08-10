---
type: adr
title: "ADR-0019: Add planned bulk removal for gone-upstream worktrees"
description: "Extend worktree remove with a local-ref-based, revalidated bulk mode without implicit fetch or branch deletion."
resource: gh-qw
tags: [gh-qw, adr, adr-0019, cli, git, worktree, safety]
timestamp: 2026-08-10
---

# ADR-0019: Add planned bulk removal for gone-upstream worktrees

- **Status:** Accepted
- **Date:** 2026-08-10
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0002](../0002-distribute-as-gh-cli-extension/), [ADR-0005](../0005-dedicated-worktree-root/), [ADR-0018](../0018-herdr-workspace-integration/)

## Context

Merged or deleted remote branches often leave registered linked worktrees behind. Removing them one
at a time requires a person to inspect branch tracking state, map a branch back to gh-qw's possibly
different deterministic slot, and repeat a destructive command. Git's human-readable `[gone]`
annotation is presentation output rather than a stable machine contract, and a command named
`cleanup` would blur this lifecycle operation with `worktree prune`, whose purpose remains stale
administrative-record cleanup.

A bulk operation also enlarges the race window between inspection and mutation. It must not infer
fresh remote state by silently fetching: gh-qw's network-capable Git boundary delegates through
deterministically selected `gh` authentication, while a local `git fetch` would introduce ambient
transport and credential behavior into a removal command.

## Decision

Extend the existing `worktree remove` command with a mutually exclusive `--gone` mode. A candidate
is a registered linked worktree attached to a branch whose configured, fully qualified upstream ref
is absent from the local Git ref database. Obtain branch and upstream fields through structured
`git for-each-ref` output and test the exact upstream with `git show-ref --verify`; never parse
`[gone]`. Use the attached branch for this lookup even when the deterministic slot differs.

Do not fetch implicitly and do not delete local branches or commits. A person who needs current
remote knowledge fetches and prunes explicitly before invoking the command. Keep `worktree prune`
focused on broken or expired worktree metadata and do not add a generic `cleanup` command.

Build and print one deterministic plan, confirm it once through the controlling terminal, rebuild
and compare the entire plan before the first deletion, and revalidate each target immediately before
mutation. Keep unsafe candidates with a reason, including any worktree whose path contains a
registration from any discovered repository, so recursive path removal cannot affect unplanned
worktree state. Retain missing registered paths for this containment check and physicalize their
longest existing prefix so symbolic-link aliases do not bypass it. Require the discovered-repository
inventory to remain unchanged through plan comparison and immediate revalidation. Continue after
candidate-specific refusals and Git removal failures so independent work can complete, but stop the
remainder when a shared boundary, repository inventory, or registered state becomes indeterminate.
`--force` is exactly Git's one force level for dirty worktrees; `--yes` skips only the bulk
confirmation.

Resolve an enabled Herdr workspace before unregistering each target and close it after successful
removal, preserving ADR-0018's ordering. This backward-compatible public CLI addition is a SemVer
MINOR change; from `v0.2.0`, absent another version-affecting change, it targets `v0.3.0`.

## Consequences

### Positive

- Gone-upstream worktrees can be reviewed and removed in one command without parsing localized Git
  presentation output or deleting reusable local history.
- Plan comparison, per-target identity checks, and partial-failure reporting make bulk mutation
  explicit and auditable.
- The command keeps network authentication and worktree-pruning boundaries unchanged.

### Negative

- Results can be stale until the caller explicitly fetches and prunes remote-tracking refs.
- A large batch performs repeated Git and filesystem validation, favoring safety over speed.

### Neutral

- A batch may remove safe candidates and still exit with status `1` because other candidates were
  retained or failed; scripts must inspect diagnostics rather than treating status `1` as rollback.
