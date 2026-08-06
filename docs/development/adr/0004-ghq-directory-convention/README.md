---
type: adr
title: "ADR-0004: Adopt the ghq directory convention with paths as the source of truth"
description: "Use ghq-compatible repository paths without custom managed metadata."
resource: gh-qw
tags: [gh-qw, adr, adr-0004, ghq, filesystem, metadata]
timestamp: 2026-08-05
---

# ADR-0004: Adopt the ghq directory convention with paths as the source of truth

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0005](../0005-dedicated-worktree-root/), [ADR-0006](../0006-command-set-v1/), [ADR-0007](../0007-configuration-sources/)

## Context

The useful invariant from ghq is a repository's predictable
`<root>/<host>/<owner>/<repo>` location. That convention makes identity visible to people, shell
tools, and Git without a separate registry.

A private catalog or custom Git configuration that marks repositories as managed, adopted,
healthy, or authoritative creates a second source of truth. It can drift from the filesystem and
from Git's own worktree data, making moves and recovery harder.

## Decision

We will place main repositories beneath configured roots using the ghq directory convention
`<host>/<owner>/<repo>`. The normalized path on disk is the repository identity and the source of
truth for discovery.

Main repositories remain ordinary Git clones. `gh-qw` will not maintain a private repository
catalog or write custom managed, adoption, identity, or health metadata. Repository and linked
worktree state will be obtained from Git when needed.

## Consequences

### Positive

- Repository locations are predictable and interoperable with ghq habits and ordinary tools.
- There is no metadata database to synchronize, migrate, or repair.
- A checkout remains understandable and usable without `gh-qw`.

### Negative

- Moving a repository outside the convention changes how `gh-qw` discovers and identifies it.
- The path convention cannot encode exceptional ownership or health classifications.

### Neutral

- Filesystem migration and Git repair, rather than custom metadata updates, handle intentional
  moves.
