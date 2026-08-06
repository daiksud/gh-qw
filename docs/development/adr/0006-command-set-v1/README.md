---
type: adr
title: "ADR-0006: Define the v1 command set"
description: "Keep the first command surface focused on repository acquisition and Git worktree lifecycle."
resource: gh-qw
tags: [gh-qw, adr, adr-0006, cli, scope, git]
timestamp: 2026-08-05
---

# ADR-0006: Define the v1 command set

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0004](../0004-ghq-directory-convention/), [ADR-0005](../0005-dedicated-worktree-root/), [ADR-0007](../0007-configuration-sources/)

## Context

`gh-qw` should preserve the small, familiar part of ghq that supports acquiring and finding
repositories, then add the common lifecycle for linked Git worktrees. Carrying every ghq option,
shell convenience, VCS abstraction, or advanced `git worktree` operation would broaden the
contract without strengthening that purpose.

The first public surface must be explicit enough to guide implementation while leaving detailed
flags and output formats to the CLI reference.

## Decision

The v1 command set will be:

- `get`, with `clone` as an alias, for acquiring repositories;
- `list`, `root`, `rm`, and `migrate` for discovery, root reporting, removal, and migration; and
- `worktree add`, `worktree list`, `worktree remove`, and `worktree prune` for the supported
  linked-worktree lifecycle.

The following are intentionally excluded:

- **`create`:** `git init` and `gh repo create` already cover repository creation; `gh-qw` focuses
  on acquiring and organizing repositories with established identities.
- **`look` and `get --look`:** spawning a shell creates portability and shell-state problems.
  `gh-qw` will output paths that callers can compose with their own shell navigation.
- **Bare repositories and `--bare`:** the model requires an ordinary main worktree and adds linked
  worktrees around it; a bare repository has no main checkout.
- **Multiple VCS implementations and `--vcs`:** linked worktrees are a Git feature, so another VCS
  abstraction would add branches of behavior without providing the core capability.
- **`worktree move`, `lock`, `unlock`, and `repair`:** these are advanced or recovery operations
  available through Git. v1 exposes the routine lifecycle only. Migration may ask Git to repair
  moved relationships internally without making repair a general public command.

## Consequences

### Positive

- The command surface remains small, coherent, and testable.
- Familiar ghq operations have clear equivalents while worktrees are first-class.
- Git remains the escape hatch for uncommon administration.

### Negative

- The CLI is not a drop-in replacement for every ghq command or flag.
- Some advanced worktree tasks require direct `git worktree` commands.

### Neutral

- Future commands require a new decision when they materially expand the product's scope.
