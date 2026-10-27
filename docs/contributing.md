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

## Repository maintenance

Use [Repository automation](repository-automation.md) for this repository's
settings, labels, required checks, and credential boundaries. The
[maintainer release guide](releases.md) owns version selection, manual
publication, artifact identity, and recovery. Merging a PR is not permission
to release.
