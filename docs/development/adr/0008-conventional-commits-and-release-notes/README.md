---
type: adr
title: "ADR-0008: Adopt Conventional Commits and automated release notes"
description: "Use Conventional Commits and GitHub-generated release notes for consistent releases."
resource: gh-qw
tags: [gh-qw, adr, adr-0008, commits, release-notes]
timestamp: 2026-08-05
---

# ADR-0008: Adopt Conventional Commits and automated release notes

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0002](../0002-distribute-as-gh-cli-extension/), [ADR-0003](../0003-go-cobra-prebuilt-binaries/)

## Context

The project needs a readable commit history and useful release summaries without maintaining a
separate changelog generator. GitHub Releases can generate notes from merged pull requests, while
Conventional Commits provide a consistent vocabulary for the changes recorded in Git.

Generated notes are categorized from pull request metadata rather than commit syntax alone, so the
commit and pull request conventions must work together.

## Decision

Commit messages and squash-merge titles will follow
[Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/), including explicit
markers for breaking changes.

The release process will use GitHub's automatically generated release notes. Pull request labels
and the repository's release-note configuration will determine categories, and labels should
remain consistent with the corresponding Conventional Commit type.

## Consequences

### Positive

- History has a predictable, machine-readable structure.
- Releases receive categorized summaries without another changelog toolchain.
- Commit intent and pull request categorization can be reviewed before merge.

### Negative

- Contributors and maintainers must keep commit types, squash titles, and labels consistent.
- Incorrect or missing labels reduce the quality of generated notes.

### Neutral

- This convention structures history and notes; it does not by itself choose version numbers.
