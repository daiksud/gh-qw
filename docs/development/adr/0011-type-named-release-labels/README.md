---
type: adr
title: "ADR-0011: Name release labels after Conventional Commits types"
description: "Replace GitHub's default labels with a Conventional Commits-named vocabulary applied automatically from branch names and paths."
resource: gh-qw
tags: [gh-qw, adr, adr-0011, commits, release-notes, labels]
timestamp: 2026-08-06
---

# ADR-0011: Name release labels after Conventional Commits types

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0008](../0008-conventional-commits-and-release-notes/) (superseded by this
  record), [ADR-0010](../0010-public-api-and-semantic-versioning/)

## Context

[ADR-0008](../0008-conventional-commits-and-release-notes/) committed `gh-qw` to Conventional
Commits and GitHub-generated release notes, but left the label vocabulary itself unspecified beyond
"labels should remain consistent with the corresponding Conventional Commit type." In practice, the
repository carried only GitHub's own default labels (`bug`, `enhancement`, `documentation`, plus
untyped triage labels), none of which are Conventional Commits types. `.github/release.yml`'s
categories never matched a label any pull request actually had, so every release note entry fell
into "Other Changes" regardless of what it contained.

[ADR-0010](../0010-public-api-and-semantic-versioning/) also requires that a release's version be
mechanically computable from the pull requests merged since the last tag, which in turn requires a
label vocabulary applied deterministically — not by hand — and unambiguous about whether a given
label affects the version.

## Decision

Except for two footer-driven exceptions, every label shares its name exactly with a Conventional
Commits type from `@commitlint/config-conventional`. `BREAKING CHANGE` and `DEPRECATED` are the
exceptions: Angular's commit convention has no dedicated type for either, declaring them instead in
a commit footer on top of whichever type describes the actual change, so they get a matching
branch-name exception instead.

Labels are applied by `actions/labeler` with `sync-labels: true`, from two independent sources:

1. The six version-affecting labels (`BREAKING CHANGE`, `DEPRECATED`, `feat`, `perf`, `fix`,
   `revert`) are matched **only** against the pull request's head branch name. The branch name is
   the one signal available before a single commit is written, and the one signal ADR-0010's tag
   verification job can also recompute independently from each merge commit's associated pull
   request.
2. The remaining labels (`docs`, `refactor`, `style`, `test`, `build`, `ci`, `chore`) are
   additionally matched against changed file paths. Path rules are barred from ever touching a
   version-affecting label, so a path match can never introduce ambiguity into version computation.

`.github/release.yml` is re-categorized to this label set. GitHub's default `enhancement`, `bug`,
and `documentation` labels are removed, since they now duplicate `feat`, `fix`, and `docs` and their
prior presence caused the original category mismatch. The exhaustive label table, color scheme, and
path globs live in the [versioning reference](../../../reference/versioning/#labels), which this
ADR does not duplicate.

## Consequences

### Positive

- Release notes categorize correctly: every merged pull request's label now matches one of
  `.github/release.yml`'s categories.
- The same label vocabulary serves release notes, human triage, and ADR-0010's automated version
  computation without maintaining a second mapping.
- `sync-labels` keeps a pull request's version-affecting label in step automatically if its branch
  is renamed before merge.

### Negative

- A contributor must name their branch with the correct Conventional Commits prefix (or one of the
  two footer exceptions) for the intended label, and therefore the intended version impact, to
  apply.
- A pull request opened from a misnamed branch needs a rename, not a manual label edit: `sync-labels`
  only trusts the branch name for version-affecting labels, so a hand-added label is removed on the
  next sync.

### Neutral

- This ADR does not restate the full label-to-category mapping, color scheme, or path-glob rules;
  see the [versioning reference](../../../reference/versioning/#labels) for the current, exhaustive
  table.
