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
[ADR template](../adr/template/). While the project is `0.y.z`, revise accepted ADRs in place when
their decisions change; after `1.0.0`, follow ADR-0001's superseding policy.

## Pull request types and labels

PR titles and commit headers follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
Use the `@commitlint/config-conventional` types: `feat`, `fix`, `perf`, `revert`,
`docs`, `refactor`, `style`, `test`, `build`, `ci`, and `chore`.

**Branch names are unrestricted.** They never determine the PR type, its labels, or
the release version. Reviewers and authors may revise the PR title and labels
without renaming a branch.

Choose the type from actual user impact: `feat` and `fix` refer to gh-qw
functionality and bugs, not to CI, docs tooling, or test changes. Reviewers
check that the chosen type matches the change. Keep the final squash commit
consistent with the reviewed PR and preserve meaningful commit footers.

The six version-affecting labels (`BREAKING CHANGE`, `DEPRECATED`, `feat`,
`perf`, `fix`, and `revert`) are reviewed metadata, editable directly at
any time. For a public API break or deprecation, apply the corresponding
label alongside the type and preserve `BREAKING CHANGE:` or `DEPRECATED:`
footers describing impact and migration. No branch prefix is required.

A labeler automatically **adds only** `docs`, `test`, `build`, and `ci`
when every changed file matches one unambiguous path category. It does not
remove manually adjusted labels and does not infer user-visible semantics.
Authors and reviewers remove stale path labels when a PR changes scope.

## Release a version

After reviewing labels on merged PRs, manually run the Release workflow from
`main`:

```console
$ gh workflow run release.yml --ref main
```

No version input is needed. The workflow selects the next version from the
latest published stable release and the reviewed labels of merged PRs, then
validates one captured `main` commit with full CI and builds all five
platform binaries. Only after both succeed does it create a tag and publish a
GitHub Release. **Pushing a tag does not trigger a release.**

A preparation, test, or build failure leaves no tag. Final publication is
not atomic: an API error during tag/release creation may leave a tag or draft.
Inspect the remote state before retrying. Published releases are immutable.
See the [versioning reference](../../reference/versioning/) for detailed rules.

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

Dependabot pull requests are enabled for squash auto-merge by
`.github/workflows/dependabot-auto-merge.yml`. The `CI success` status check is required by the
default branch ruleset, so a dependency update cannot merge when the Go checks, Astro validation,
runtime smoke test, settings validation, or release-preparation tests fail. The auto-merge workflow is deliberately
separate from the validation workflow and does not check out pull request code.

When changing the required workflow context, merge the workflow change before applying the
corresponding settings manifest. After the new context exists on `main`, run `gh infra plan` and
`gh infra apply` locally. Existing Dependabot pull requests may need a rebase to report the new
context.

## Validate changes

Run the relevant Go checks from the repository root:

```console
$ go test ./...
```

For release automation changes, also run the offline tests:

```console
$ bash .github/scripts/prepare-release.test.sh
```

For documentation-site changes, use Node.js and pnpm:

```console
$ cd .pages
$ vp install --frozen-lockfile
$ vpr validate
```

`vpr validate` includes the Astro runtime smoke test. It serves the generated site locally and
checks the HTML routes, homepage assets, Pagefind, and not-found behavior in addition to the
static build and link checks.

The package manager remains pnpm as Vite+'s backend. Use `vpr` for the site tasks; `vp dev` and
`vp build` are Vite+'s built-in Vite commands and do not run Astro.

Changes to `docs/` or `.pages/` merged into `main` trigger the GitHub Pages deployment workflow.
The published site is [https://daiksud.github.io/gh-qw/](https://daiksud.github.io/gh-qw/).

Do not commit generated `.pages/node_modules/`, `.pages/.astro/`, or `.pages/dist/` content.
Use relative documentation links and follow the
[versioning reference](../../reference/versioning/) and
[ADR-0011](../adr/0011-type-named-release-labels/).
