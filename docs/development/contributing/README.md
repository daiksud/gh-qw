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
[compatibility reference](../../reference/compatibility/),
[repository settings reference](../../reference/repository-settings/), and
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
top of whichever type describes the actual change. For `feat`, `perf`, and `fix`, put the matching
exception prefix before the type so label automation can apply both labels:

| Footer | Branch prefix | Labels |
| --- | --- |
| `BREAKING CHANGE: <summary>` | `breaking-change/{feat,perf,fix}/short-description` | `BREAKING CHANGE` + type |
| `DEPRECATED: <what is deprecated>` | `deprecated/{feat,perf,fix}/short-description` | `DEPRECATED` + type |

The type segment is required. Bare `breaking-change/` or `deprecated/` branches, combinations with
other types, and stacked exception prefixes are invalid and receive no version-affecting label.
When both labels are present, `BREAKING CHANGE` takes precedence for release notes and version
calculation. The branch must still contain the matching footer in at least one commit.

A GitHub Actions labeler applies labels automatically from the pull request's branch name and, for
a few non-version-affecting types, from its changed file paths. Do not hand-edit a
version-affecting label (`BREAKING CHANGE`, `DEPRECATED`, `feat`, `perf`, `fix`, `revert`) — it is
resynchronized from the branch name on every push, so a mismatched branch name needs a rename, not
a label edit.

Every merged pull request's label feeds both the generated release notes and a tag-verification
job that computes the next release version from those labels and fails the release if a pushed tag
does not match. See the [versioning reference](../../reference/versioning/) for the full public API
declaration, the `0.y.z` version-selection rule, and the exhaustive label table.

## Repository settings

`.github/settings.yml` declares GitHub repository settings — labels, merge strategy, security
options, and rulesets — as a `gh-infra` manifest. To change a setting:

1. Edit `.github/settings.yml` and open a pull request.
2. CI validates the manifest's syntax and schema. It does not check for drift against live GitHub
   state — before or after merge, run `gh infra plan .github/settings.yml` locally to review the
   diff like any other code change.
3. A maintainer applies the change from a local machine:

   ```console
   $ gh extension install babarot/gh-infra --pin v0.13.0
   $ gh infra plan .github/settings.yml
   $ gh infra apply .github/settings.yml
   ```

Always pass the file path, not the `.github/` directory, to `gh-infra` — a directory argument also
scans non-manifest YAML such as `labeler.yml`, which `gh-infra` silently skips by default, but the
file path avoids relying on that behavior. Neither `plan` nor `apply` ever runs in CI; see
[ADR-0012](../adr/0012-github-settings-as-code/) for why.

`.github/settings.yml` reconciles labels and rulesets **authoritatively**: an entry removed from
the manifest is deleted on GitHub, not merely left untracked. Always review `gh infra plan`'s
output before running `apply`. See the
[repository settings reference](../../reference/repository-settings/) for the full contract.

## Validate changes

Run the relevant Go checks from the repository root:

```console
$ go test ./...
```

For documentation-site changes, use Node.js and pnpm:

```console
$ cd .pages
$ vp install --frozen-lockfile
$ vpr validate
```

The package manager remains pnpm as Vite+'s backend. Use `vpr` for the site tasks; `vp dev` and
`vp build` are Vite+'s built-in Vite commands and do not run Astro.

Changes to `docs/` or `.pages/` merged into `main` trigger the GitHub Pages deployment workflow.
The published site is [https://daiksud.github.io/gh-qw/](https://daiksud.github.io/gh-qw/).

Do not commit generated `.pages/node_modules/`, `.pages/.astro/`, or `.pages/dist/` content.
Use relative documentation links and follow the
[versioning reference](../../reference/versioning/) and
[ADR-0011](../adr/0011-type-named-release-labels/).
