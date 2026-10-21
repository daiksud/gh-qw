---
type: adr
title: "ADR-0011: Name release labels after Conventional Commits types"
description: "Use reviewed Conventional Commit labels and supplemental path-based labels independent of branch names."
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

[ADR-0010](../0010-public-api-and-semantic-versioning/) requires
explicit labels to compute the next release version from merged PRs.
Branch names are chosen before reviewers know the full semantic impact.
Editing release labels and PR types must not require renaming a branch.

## Decision

PR titles, commit headers, and type labels use the Conventional Commits
vocabulary from `@commitlint/config-conventional`. `BREAKING CHANGE`
and `DEPRECATED` supplement type labels with compatibility information.
Their impact and migration explanations are retained in final squash
commit footers.

Authors and reviewers assign and edit the six version-affecting labels:
`BREAKING CHANGE`, `DEPRECATED`, `feat`, `perf`, `fix`, and `revert`.
Reviewers check that the title type and labels reflect actual impact
on gh-qw users. CI features or fixes use maintenance types rather than
pretending to add features or fix bugs in the distributed tool.

Branch names have **no required type prefix** and do not determine any
label or version. The labeler automatically adds only the supplemental
`docs`, `test`, `build`, and `ci` labels from file paths when every
changed file falls into that category. `sync-labels: false` ensures it
does not remove manually reviewed labels. Reviewers remove obsolete
path labels when a PR's scope changes. `refactor`, `style`, and
`chore` also remain manual classifications.

Dependabot assigns initial `build` or `ci` labels and matching title
prefixes but does not configure branch naming. Its changes are
reclassified as needed during review. Release notes and version
calculation use the reviewed PR labels, not branch names. See the
[versioning reference](../../../reference/versioning/) and
[labeler configuration](https://github.com/daiksud/gh-qw/blob/main/.github/labeler.yml)
for the complete mapping.

## Consequences

### Positive

- A mistaken type or release label is corrected without creating a new PR.
- One vocabulary covers PR titles, labels, and release notes.
- Simple file-only changes are classified automatically without guessing
  whether a code change is a feature, fix, or refactor.
- Labeler runs never remove manual classifications.

### Negative

- Authors and reviewers must spot missing or incorrect semantic labels.
- Stale automatically added labels are removed manually after scope changes.
- Labels can change after merge, so check them before a manual release.

### Neutral

- Version tiers and release-note categories remain unchanged.
