---
type: index
title: "Contributing"
description: "Focused contribution and validation guidance for gh-qw."
resource: gh-qw
tags: [gh-qw, development, contributing]
timestamp: 2026-08-05
---

# Contributing

Keep changes focused and preserve the contracts in the [concept](../../concept/),
[CLI reference](../../reference/cli/), [configuration reference](../../reference/configuration/),
[versioning reference](../../reference/versioning/),
[compatibility reference](../../reference/compatibility/), and
[Architecture Decision Records](../adr/). Record a future durable choice by copying the
[ADR template](../adr/template/) and follow ADR-0001's immutability policy after acceptance.

## Branch names, commits, and labels

Name every branch and write every commit header with a
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) type from
[`@commitlint/config-conventional`](https://github.com/conventional-changelog/commitlint/blob/master/%40commitlint/config-conventional/README.md):
`feat`, `fix`, `perf`, `revert`, `docs`, `refactor`, `style`, `test`, `build`, `ci`, or `chore`. A
branch starts with that type as its prefix — `feat/short-description`, `fix/short-description`, and
so on — so the type is visible before the branch is even opened.

A breaking change or a deprecation has no dedicated type; declare it in a commit footer instead, on
top of whichever type describes the actual change, and use the matching branch prefix so label
automation can see it:

| Footer | Branch prefix |
| --- | --- |
| `BREAKING CHANGE: <summary>` | `breaking-change/short-description` |
| `DEPRECATED: <what is deprecated>` | `deprecated/short-description` |

A GitHub Actions labeler applies labels automatically from the pull request's branch name and, for
a few non-version-affecting types, from its changed file paths. Do not hand-edit a
version-affecting label (`BREAKING CHANGE`, `DEPRECATED`, `feat`, `perf`, `fix`, `revert`) — it is
resynchronized from the branch name on every push, so a mismatched branch name needs a rename, not
a label edit.

Every merged pull request's label feeds both the generated release notes and a tag-verification
job that computes the next release version from those labels and fails the release if a pushed tag
does not match. See the [versioning reference](../../reference/versioning/) for the full public API
declaration, the `0.y.z` version-selection rule, and the exhaustive label table.

## Validate changes

Run the relevant Go checks from the repository root:

```console
$ go test ./...
```

For documentation-site changes, use Bun:

```console
$ cd .pages
$ bun ci
$ bun run validate
```

Do not commit generated `.pages/node_modules/`, `.pages/.astro/`, or `.pages/dist/` content.
Use relative documentation links and follow the
[versioning reference](../../reference/versioning/) and
[ADR-0011](../adr/0011-type-named-release-labels/).
