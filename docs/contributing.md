# Contributing

Write repository content, commits, issues, PRs, and reviews in English. Follow
[AGENTS.md](../AGENTS.md), the [CLI](cli.md), [configuration](configuration.md),
and [architecture](architecture.md) contracts.

Maintain only the current specification. Update the owning document when
behavior changes; do not add historical comparisons, transition guides,
compatibility shims, or duplicate implementations. Remove unreachable APIs and
tests that serve only deleted behavior. Git history records previous states.

## Issues and pull requests

Use the [Issue forms](../.github/ISSUE_TEMPLATE) for bug reports, user-facing
features or improvements, and maintenance tasks. Bug reports need actual and
expected behavior, reproduction details, and a version or ref. Feature requests
describe the problem and observable success criteria. Tasks explain the
maintenance problem and completion criteria. Blank issues remain available.

PR titles follow Conventional Commits. The [PR template](../.github/pull_request_template.md)
provides **Why**, **What changed**, and **Verification** so the reviewed PR body
can become the squash commit message. State the problem, resulting behavior,
material decisions, and observed verification results without depending on an
Issue or CI log for essential context. Include `Closes #123` only when the PR
completes the Issue, and remove template comments and temporary notes before
merging.

## Development and review

Use the Go version in [go.mod](../go.mod). Run:

```sh
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest discover -s .github/scripts -p "test_release_version.py" -q
bash .github/scripts/ci-artifacts.test.sh
bash .github/scripts/release-source.test.sh
bash .github/scripts/release-notes.test.sh
bash .github/scripts/release-artifacts.test.sh
```

PRLint only passes the PR title, a blank line, and the PR body to commitlint
using the unmodified `@commitlint/config-conventional` preset. It has no
additional rules or policy self-tests.

Keep changes focused and test observable behavior. Preserve safety boundaries,
error causes, stdout/stderr ownership, and partial results. Delegate Git/gh
semantics to the external tools. Do not add fake-only production paths.

Review security, correctness, data-loss risks, concurrency, and current-contract
coverage. Resolve actionable findings, pass required CI on the final HEAD, merge
normally, and verify CI on the actual merged commit. Record any unavailable
review or validation honestly. Administrative bypass is disabled. Follow the review and approval policy
in [AGENTS.md](../AGENTS.md#review-approvals) even when reviewers are unavailable.

Documentation is plain Markdown. Use relative links, check paths and anchors,
and keep each rule in one owning document. No documentation build is required.

## PR classification

Use Conventional Commit titles with `feat`, `fix`, `perf`, `revert`, `docs`,
`refactor`, `style`, `test`, `build`, `ci`, or `chore`. Each focused PR
has one type. `feat`, `fix`, and `perf` concern user-observable gh-qw
behavior, not improvements to internal CI or build tooling. Branch names are
identifiers, not classification inputs.

The same 20 labels are used in gh-settings: eleven change types, the
compatibility labels `BREAKING CHANGE` and `DEPRECATED`, and seven independent
triage labels (`accessibility`, `duplicate`, `good first issue`,
`help wanted`, `invalid`, `question`, and `wontfix`).
[Repository settings](../.github/settings.yml) owns their descriptions and
colors. Compatibility labels can accompany the change type. Reviewers use the
final diff and its product impact, not only paths or the Issue's initial type.

The [Labeler](../.github/labeler.yml) suggests `docs`, `test`, `build`, or
`ci` only when **every changed file** matches one category. Docs cover root
guides, `docs/**`, skill instructions and PR templates; tests cover
`tests/**` and `**/*_test.go`; build covers `go.mod`, `go.sum`, and
`mise.toml`; CI covers CI/PRLint/Labeler workflows, and CI-specific local
Actions/scripts. Release tooling, other workflows, public interfaces, and mixed
changes require manual classification. Hidden paths are included; labels are
add-only, so reviewers remove stale suggestions. Dependabot proposes `build`
for Go modules and `ci` for GitHub Actions updates, without automatic merging.

[Issue forms](../.github/ISSUE_TEMPLATE) initially label bug reports `fix` and
feature requests `feat`. Tasks and blank issues have no initial label; their
eventual implementation PRs can use a different type.

The independent [PRLint workflow](../.github/workflows/prlint.yml) validates the
PR title and optional body together as the proposed merge commit message. It
checks Conventional Commits with one type, an optional scope, and a nonempty
description under the preset's default rules. A `!` header or
`BREAKING CHANGE:` footer marks an incompatibility. Passing syntax validation
does not establish correct semantic classification. Squash messages determine
versions; reviewed PR labels classify release notes.

## Repository settings

[.github/settings.yml](../.github/settings.yml) declares the supported
repository fields, labels, branch and tag Rulesets, Actions policies, and
private vulnerability reporting using
[gh-settings](https://github.com/daiksud/gh-settings) v0.21.0.

The pinned gh-settings Action validates this manifest in the Linux commit checks
for PR CI, main CI, and release builds. Validation is offline: it neither
detects remote drift nor applies GitHub settings. These workflows need no
administrative credentials and do not mutate repository settings.

A maintainer reviews and applies settings locally with an appropriately
authorized GitHub CLI identity:

```sh
gh extension install daiksud/gh-settings
gh extension upgrade gh-settings
gh settings validate
gh settings plan
gh settings apply
```

Run commands from this repository's checkout. `plan` is nonmutating; review
all reported differences before running `apply`. Apply changes directly,
without a stored plan, automatic rollback or atomic transaction. A failed
apply may have already changed earlier resources, so inspect the live state
and plan again before retrying.

Labels and Rulesets are **ID-selected** in the gh-settings declaration.
Their IDs belong to gh-qw, and unlisted objects are left untouched; no label
or Ruleset deletion is implied. The existing Rulesets keep required signed
commits, linear history, CODEOWNER review, one eligible approval, stale review
dismissal, review-thread resolution, and the `ci-pr` and `prlint` GitHub
Actions checks, without bypass actors. The tag Ruleset continues preventing
tag deletion and non-fast-forward changes.

The legacy manifest also specified repository visibility, release
immutability, vulnerability alerts, and automated security fixes.
gh-settings v0.21.0 does not manage these controls. Their live settings
are **not changed** by this declaration and must be maintained separately
in GitHub. Inactive normal-merge commit-message defaults are also left
unmanaged while normal merges remain disabled.

Only squash merges are allowed. The squash commit uses the reviewed PR title
and body. The Actions approval setting permits the
[Codex approval workflow](../.github/workflows/approve-codex-review.yml)
to produce a valid GitHub Actions approving review when its current-HEAD
checks pass. Copilot's reviews and Codex comments alone do not approve PRs.
A successful validation job does not prove the manifest has been applied;
verify the live Ruleset and settings separately before claiming enforcement.

Required PR checks are `ci-pr` and `prlint`, provided by GitHub Actions.
The `ci-pr` commit stage builds one candidate on each native platform; its
acceptance stage verifies and tests those exact transferred bytes without
rebuilding the product. PR code and base changes move through Draft, Commit
stage, Ready, and Acceptance stage in one run. Title/body edits rerun both
PRLint and staged code validation, avoiding skipped-success required checks.
The native `ci-pr` aggregate uses `always()` and requires every prerequisite
to succeed, including after cancellation; it has no write token. The
`ci-push` workflow runs the same Commit and Acceptance stages for `main`
and finishes with its own `ci-push` aggregate job.
Fork and Dependabot pull requests that cannot change Draft/Ready state are not
supported; a maintainer must handle them manually without a privileged fallback.

Together the staged workflows preserve Go builds and tests on Linux amd64,
macOS arm64, and Windows amd64, plus race, format/vet, settings validation, and
release-version tests. Dependabot checks Go modules and GitHub Actions weekly.
Its PRs require a manual merge decision after review and required checks; no
workflow automatically enables auto-merge. The repository-level auto-merge
option remains available.

## Releases

Merging does not authorize releasing. Only on an explicit release request, run:

```sh
gh workflow run release.yml --ref main
```

[The workflow](../.github/workflows/release.yml) uses the main commit captured
when it is dispatched and fails if main moves before resolution. An optional
reviewed source pin is only an additional equality check; it cannot select a
different commit. The workflow then
runs three product stages: commit-stage cross-builds three binaries from that
source, acceptance-stage tests the transferred binaries on native Linux amd64,
macOS arm64, and Windows amd64 runners, and release publishes only those
accepted bytes. Each internal artifact carries a source, version, asset-name,
and SHA-256 manifest; publication revalidates the complete inventory without
rebuilding. The final verification has no write token; its public API and asset
downloads are unauthenticated after read-only artifact retrieval. It compares
all three published assets with the accepted manifests. A failed
metadata/build/acceptance/publication check creates no new release.

| Commit signal | Version increment |
| --- | --- |
| Breaking header `!`, `BREAKING CHANGE` or `BREAKING-CHANGE` footer | Minor while the major version is zero; otherwise major. |
| `feat`, `perf`, or `DEPRECATED` footer | Minor. |
| `fix`, `revert`, or another substantive commit | Patch. |

The highest tier wins once for the range. The shared Python calculator runs
inside release metadata resolution; no version file or preparation PR is needed.
Empty commits do not
change the selected source and are ignored; a preparation-named commit with a
tree change remains substantive. No substantive changes, an invalid baseline,
a changed published baseline, or a conflicting tag stops publication. There is
no version override, and v0 does not automatically graduate to v1. PR labels
categorize generated release notes but do not determine the version. Tags are
not moved and published releases/assets are not replaced. Final API operations
are not atomic; inspect remote state before retrying interrupted publication.
[.github/release.yml](../.github/release.yml) owns release-note categories.
