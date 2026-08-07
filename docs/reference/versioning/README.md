---
type: reference
title: "Versioning reference"
description: "Normative public API declaration, SemVer policy, and Conventional Commits/label contract for gh-qw releases."
resource: gh-qw
tags: [gh-qw, reference, versioning, semver, conventional-commits, release]
timestamp: 2026-08-06
---

# Versioning reference

This page is the normative contract for what counts as `gh-qw`'s public API, how a release version
number is chosen, and how branch names, commit types, and labels drive that choice. It follows
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
5. process exit statuses (`0`, `1`, `2`, and `list --fzf`'s `130`);
6. configuration keys and environment variables, and their precedence (`root`, `worktree_root`,
   `GHQW_ROOT`, `GHQW_WORKTREE_ROOT`);
7. the on-disk layout and identity forms (`<root>/<host>/<owner>/<repo>`, `<canonical>@<slot>`); and
8. the external-tool prerequisites a command depends on (`gh` authentication for `get`/`worktree
   add`; the `fzf` executable for `list --fzf`).

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
1.0.0 sense. `gh-qw` nonetheless applies SemVer's ordinary clauses 6 through 8 during `0.y.z`, one
tier down from where they would land after `1.0.0`:

| Change kind | Ordinary SemVer (`x.y.z`, `x > 0`) | `gh-qw` today (`0.y.z`) |
| --- | --- | --- |
| Backward-incompatible public API change | MAJOR | **MINOR** |
| Backward-compatible public API deprecation | MINOR | **PATCH** |
| Backward-compatible public API addition | MINOR | **PATCH** |
| Substantial internal improvement, including performance ("speed is a feature") | MAY be MINOR | **PATCH** |
| Backward-compatible bug fix | PATCH | **PATCH** |
| No public API impact | no bump | no bump |

`gh-qw` remains `0.y.z` under this policy even across a backward-incompatible change: that change
still only bumps MINOR, exactly like every other version-affecting change during `0.y.z`. Declaring
`1.0.0` — and with it, the point where the "ordinary SemVer" column starts applying without the
one-tier shift — is a deliberate, separate decision that this automatic mechanism never makes by
itself; it requires its own future decision (and an update to the [tag version enforcement
job](#tag-version-enforcement) that computes versions under the current policy).

> [!IMPORTANT]
> Once released, a version's contents are never modified (SemVer clause 3). A mistaken release is
> fixed with a new version, never by replacing a tag, rebuilding its assets, or rewriting its
> release notes.

## Conventional Commits and branch naming

Every commit uses a [Conventional Commits][conventional-commits] header, and every pull request
branch starts with its commits' type as a prefix, so the type is visible before the branch is even
opened. Types follow [`@commitlint/config-conventional`][commitlint-conventional]:

| Type | Meaning | Branch prefix |
| --- | --- | --- |
| `feat` | A new feature | `feat/*` |
| `fix` | A bug fix | `fix/*` |
| `perf` | A code change that improves performance | `perf/*` |
| `revert` | Reverts a previous commit | `revert/*` |
| `docs` | Documentation only changes | `docs/*` |
| `refactor` | A code change that neither fixes a bug nor adds a feature | `refactor/*` |
| `style` | Changes that do not affect the meaning of the code | `style/*` |
| `test` | Adding missing tests or correcting existing tests | `test/*` |
| `build` | Changes to the build system or external dependencies | `build/*` |
| `ci` | Changes to CI configuration files and scripts | `ci/*` |
| `chore` | Other changes that don't modify `src` or test files | `chore/*` |

### Breaking changes and deprecations

Angular's own commit convention, which `@commitlint/config-conventional` follows, has no dedicated
type for a breaking change or a deprecation. Both are declared in a commit **footer** instead, on
top of whichever type actually describes the change:

```text
feat(list): remove --old-flag

BREAKING CHANGE: <summary>

<description and migration instructions>
```

```text
feat(get): mark --legacy-mode for removal

DEPRECATED: <what is deprecated>

<description and recommended update path>
```

Label automation (below) only ever inspects branch names and changed file paths, never commit
footers. So each of these footers gets one more branch-name exception, keeping its effect visible
without parsing commit text:

| Footer | Branch prefix | Label |
| --- | --- | --- |
| `BREAKING CHANGE:` | `breaking-change/*` | `BREAKING CHANGE` |
| `DEPRECATED:` | `deprecated/*` | `DEPRECATED` |

A pull request using either prefix must carry the matching footer in at least one of its commits.

## Labels

Every label besides the two footer exceptions above shares its name exactly with a Conventional
Commits type, so there is only one vocabulary to remember across commits, branches, and labels. A
merged pull request's labels drive both the generated release notes and the tag version check
below.

### Branch-derived labels

Only six labels ever affect the computed version, and they are applied **only** from the pull
request's head branch name, never from changed file paths:

| Label | `0.y.z` impact |
| --- | --- |
| `BREAKING CHANGE` | MINOR |
| `DEPRECATED` | PATCH |
| `feat` | PATCH |
| `perf` | PATCH |
| `fix` | PATCH |
| `revert` | PATCH |

### Path-derived labels

The remaining seven labels (`docs`, `refactor`, `style`, `test`, `build`, `ci`, `chore`) can also be
applied automatically from the paths a pull request changes, in addition to matching its branch
name. Path rules never touch the six branch-derived labels above, so a path match can never
introduce ambiguity into the version computation.

### Dependabot pull requests

`.github/dependabot.yml` assigns no labels of its own (`labels: []` on every ecosystem); a
dependency-update pull request is labeled exactly like any other, through `labeler.yml`. Its branch
name and commit/PR title both start with a Conventional Commits type — `build` for the `gomod` and
`npm` ecosystems, `ci` for `github-actions` — so the branch-name rule and the path rule agree on the
same label, and the title itself (`build(deps): bump ...`, `ci(deps): bump ...`) is Conventional
Commits-compliant. Since `build` and `ci` are both path-derived, never branch-derived-and-
version-affecting, a dependency update can never affect the computed release version.

### Label colors

Label color encodes both the SemVer tier and whether a label affects the version:

- **`BREAKING CHANGE`** uses solid red — the MAJOR tier (MINOR today, under `0.y.z`).
- **`DEPRECATED`** uses solid yellow, deliberately outside the red/green/blue tiers below: it is a
  footer-driven exception rather than a real Conventional Commits type, and yellow marks it as an
  exception at a glance.
- **`feat`** and **`perf`** use two shades of green (the MINOR tier; PATCH today under `0.y.z`),
  with `feat` the darker of the two.
- **`fix`** and **`revert`** use two shades of blue (the PATCH tier), with `fix` the darker of the
  two.
- The seven [path-derived labels](#path-derived-labels) use purple, teal, and gray tones outside
  red, green, yellow, and blue entirely, so a version-impacting label is always identifiable by hue
  alone.

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

## Tag version enforcement

Pushing a `v<major>.<minor>.<patch>` tag runs a verification job before any release artifact is
built:

1. it resolves the nearest reachable earlier version tag;
2. it resolves every pull request merged since that tag through its squash-merge commit, and
   collects their [branch-derived labels](#branch-derived-labels);
3. it computes the expected next version from those labels using the
   [`0.y.z` policy](#versioning-during-major-version-zero) above; and
4. it compares that expected version against the pushed tag.

The job fails the workflow, and no release is created, when:

- the pushed tag does not equal the computed version; or
- no pull request merged since the last release carries a branch-derived label.

There is no override. A release that should not have happened is never edited or re-tagged (see
[the immutability rule above](#versioning-during-major-version-zero)); the next correct tag is
pushed instead.
