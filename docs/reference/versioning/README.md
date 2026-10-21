---
type: reference
title: "Versioning reference"
description: "Normative public API declaration, SemVer policy, and Conventional Commits/label contract for gh-qw releases."
resource: gh-qw
tags: [gh-qw, reference, versioning, semver, conventional-commits, release]
timestamp: 2026-08-09
---

# Versioning reference

This page is the normative contract for what counts as `gh-qw`'s public API, how a release version
number is chosen, and how reviewed pull request labels drive that choice. It follows
[Semantic Versioning 2.0.0](https://semver.org/) and [Conventional Commits][conventional-commits],
using the commit types defined by [`@commitlint/config-conventional`][commitlint-conventional]. See
[ADR-0010](../../development/adr/0010-public-api-and-semantic-versioning/) and
[ADR-0011](../../development/adr/0011-type-named-release-labels/) for the design rationale, and the
[compatibility reference](../compatibility/) for how current behavior maps to implementation and
test evidence.

[conventional-commits]: https://www.conventionalcommits.org/en/v1.0.0/
[commitlint-conventional]: https://github.com/conventional-changelog/commitlint/blob/master/%40commitlint/config-conventional/README.md

## Public API

SemVer 2.0.0 requires a declared public API (clause 1). For `gh-qw`, the public API is:

1. command and subcommand names, and their aliases (for example, `get`'s `clone` alias);
2. flag names, short forms, meaning, and default values;
3. positional-argument forms and repository-specification syntax;
4. the `stdout` output contract: content, format, ordering, and separator normalization;
5. process exit statuses (`0`, `1`, and `2`);
6. configuration keys and environment variables, and their precedence (`root`, `worktree_root`,
   `GHQW_ROOT`, `GHQW_WORKTREE_ROOT`);
7. the on-disk layout and identity forms (`<root>/<host>/<owner>/<repo>`, `<canonical>@<slot>`); and
8. the external-tool prerequisites a command depends on (`gh` authentication for `get`/`worktree
   add`).

A version-affecting change is one that changes one of the above. The following are explicitly
**not** part of the public API and never affect the version number by themselves:

- `stderr` diagnostic and progress message wording;
- the `internal/` Go package layout and any exported Go identifier within it — `gh-qw` is
  distributed only as prebuilt binaries (see
  [ADR-0003](../../development/adr/0003-go-cobra-prebuilt-binaries/)) and has no Go API contract;
- documentation content;
- CI configuration; and
- test code.

## Versioning during major version zero

`gh-qw` is currently `0.y.z`. Per SemVer clause 4, "anything MAY change at any time" during initial
development, and the public API declared above should not yet be considered stable in the SemVer
1.0.0 sense. `gh-qw` nonetheless applies the ordinary MINOR and PATCH rules during `0.y.z`. The
only exception is that a change which would ordinarily increment MAJOR increments MINOR instead,
so the major version remains zero:

| Change kind | Ordinary SemVer (`x.y.z`, `x > 0`) | `gh-qw` (`0.y.z`) |
| --- | --- | --- |
| Backward-incompatible public API change | MAJOR | **MINOR** |
| Backward-compatible public API deprecation | MINOR | **MINOR** |
| Backward-compatible public API addition | MINOR | **MINOR** |
| Substantial internal improvement, including performance ("speed is a feature") | MINOR | **MINOR** |
| Backward-compatible bug fix | PATCH | **PATCH** |
| No public API impact | no bump | no bump |

For example, the backward-compatible `worktree remove --gone` public CLI addition is a MINOR
change. If `v0.2.0` is still the latest release when it ships and no other change selects a
different version, the resulting version is `v0.3.0` (see
[ADR-0019](../../development/adr/0019-remove-gone-worktrees/)).

`gh-qw` remains `0.y.z` under this policy even across a backward-incompatible change: that change
bumps MINOR rather than MAJOR. Declaring `1.0.0` is a deliberate, separate decision that this
automatic mechanism never makes by itself. A nonzero major version requires a deliberate graduation decision and a separate
policy/implementation update. Once the project is past that transition, the release workflow applies the
ordinary MAJOR, MINOR, and PATCH rules.

> [!IMPORTANT]
> Once released, a version's contents are never modified (SemVer clause 3). A mistaken release is
> fixed with a new version, never by replacing a tag, rebuilding its assets, or rewriting its
> release notes.

## Conventional Commits and review

PR titles and commit headers use [Conventional Commits][conventional-commits] types
from [`@commitlint/config-conventional`][commitlint-conventional]:
`feat`, `fix`, `perf`, `revert`, `docs`, `refactor`, `style`,
`test`, `build`, `ci`, and `chore`.

Branch names are unrestricted and do not determine labels or release versions.
Authors and reviewers choose PR types and labels according to changes to the
[public API](#public-api). A feature or fix in CI or tests does not itself
constitute a `feat` or `fix` release of gh-qw. During review, titles and
labels may be corrected without renaming the branch.

### Breaking changes and deprecations

Declare a breaking public API change with a `BREAKING CHANGE:` footer and a
deprecation with `DEPRECATED:`. Apply the corresponding label alongside the
chosen type and preserve the impact and migration guidance in the final
squash commit. Neither requires a special branch prefix, and both can
accompany any appropriate type.

## Labels

PR labels drive version selection and generated release notes. Their names,
colors, and descriptions are declared in
[repository settings](../repository-settings/).

### Reviewed version-affecting labels

| Label | `0.y.z` impact | After `1.0.0` |
| --- | --- | --- |
| `BREAKING CHANGE` | MINOR | MAJOR |
| `DEPRECATED` | MINOR | MINOR |
| `feat`, `perf` | MINOR | MINOR |
| `fix`, `revert` | PATCH | PATCH |

Authors and reviewers add, remove, and correct these labels manually. The
highest tier among the PRs included in a release determines the next
version. `BREAKING CHANGE` wins over an accompanying type. Automation never
infers these six labels from branch names, PR titles, commits, or paths.

### Path-derived labels

The labeler automatically adds `docs`, `test`, `build`, and `ci` only
when **all changed files** fall within one corresponding path category. The
exact file patterns live in
[the labeler configuration](https://github.com/daiksud/gh-qw/blob/main/.github/labeler.yml).
Mixed changes remain classified by reviewers; `refactor`, `style`, and
`chore` are also manual. Path labels do not affect versions by themselves.

Label synchronization is add-only (`sync-labels: false`), so manual labels
are not removed by automation. A matching path label may be added again on
a subsequent push. Authors and reviewers remove stale path labels after
a PR's changed files evolve.

### Dependabot pull requests

Dependabot explicitly assigns `build` to Go and npm updates and `ci` to
GitHub Actions updates. Conventional Commit prefixes remain in generated
titles; Dependabot's branch names have no classification meaning. Authors
and reviewers add version-affecting labels if a dependency change actually
modifies gh-qw's user-visible behavior.

### Label colors

`BREAKING CHANGE` is red, `DEPRECATED` yellow, `feat` and `perf` green,
and `fix` and `revert` blue. Non-version labels use other colors; the
precise values live in the repository settings manifest.

## Release notes

`.github/release.yml` groups merged pull requests into categories by label, in this order (each
pull request's first matching category wins):

1. 💥 BREAKING CHANGE
2. ⚠️ DEPRECATED
3. 🚀 Features (`feat`, `perf`)
4. 🐛 Bug Fixes (`fix`)
5. ⏪ Reverts (`revert`)
6. 📝 Documentation (`docs`)
7. 🧹 Maintenance (`refactor`, `style`, `test`, `build`, `ci`, `chore`)
8. 🔧 Other Changes (anything else)

No pull request is excluded from generated notes: every merge is visible in the release it ships
in.

## Manual release workflow

Start the Release workflow from `main`:

```console
$ gh workflow run release.yml --ref main
```

The workflow accepts no version input, and tag pushes do not trigger it.
The selected commit SHA is fixed for version preparation, CI, binary
builds, and publishing even if `main` advances.

1. Find the latest **published stable** GitHub Release and its tag.
   Unpublished or failed tags do not select the baseline.
2. Resolve commits between that tag and the selected commit, find PRs merged
   to `main`, and collect their reviewed version-affecting labels.
3. Compute the next version using the policy above. Abort when the baseline
   or its ancestry cannot be resolved, an API call fails, or there are no
   version-affecting PR labels.
4. Run the full reusable CI workflow and build all five platform binaries
   against the selected commit. A failure creates no tag.
5. Only after those checks succeed, create the computed tag and GitHub
   Release, attach binaries, and generate notes from the previous release.

The workflow fails if an existing tag points at another commit, or if the
published baseline changes while it is running. Existing tags are not
moved, and published releases and assets are never replaced. The final
GitHub API operations are not atomic; an interrupted publication may
leave a tag or draft release. Inspect remote state before retrying.

The workflow translates reviewed classification into a version; it
cannot itself decide the semantic impact of source changes.
