# Contributing

Write repository content, commits, issues, PRs, and reviews in English. Follow
[AGENTS.md](../AGENTS.md), the [CLI](cli.md), [configuration](configuration.md),
and [architecture](architecture.md) contracts.

Maintain only the current specification. Update the owning document when
behavior changes; do not add historical comparisons, transition guides,
compatibility shims, or duplicate implementations. Remove unreachable APIs and
tests that serve only deleted behavior. Git history records previous states.

## Development and review

Use the Go version in [go.mod](../go.mod). Run:

```sh
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
bash .github/scripts/prepare-release.test.sh
bash .github/scripts/ci-artifacts.test.sh
bash .github/scripts/release-source.test.sh
bash .github/scripts/release-notes.test.sh
bash .github/scripts/release-artifacts.test.sh
```

Install the pinned PR message validation tools with `mise install`, then run:

```sh
bash tests/github-actions/commitlint.sh
```

Keep changes focused and test observable behavior. Preserve safety boundaries,
error causes, stdout/stderr ownership, and partial results. Delegate Git/gh
semantics to the external tools. Do not add fake-only production paths.

Review security, correctness, data-loss risks, concurrency, and current-contract
coverage. Resolve actionable findings, pass required CI on the final HEAD, merge
normally, and verify CI on the actual merged commit. Record any unavailable
review or validation honestly. Do not bypass protections.

Documentation is plain Markdown. Use relative links, check paths and anchors,
and keep each rule in one owning document. No documentation build is required.

## PR classification

Use Conventional Commit titles with `feat`, `fix`, `perf`, `revert`, `docs`,
`refactor`, `style`, `test`, `build`, `ci`, or `chore`. `feat` and `fix` describe
user-visible gh-qw changes, not additions to CI or tests. Branch names are
identifiers only and do not determine labels or versions.

Reviewers maintain semantic labels. The labeler adds `docs`, `test`, `build`, or
`ci` only when every changed path matches a single category; remove stale labels
when scope changes. Dependabot uses `build` for Go and `ci` for Actions updates.
Keep the merge title and body consistent with the reviewed change.

The independent [PRLint workflow](../.github/workflows/prlint.yml) validates the
PR title and optional body together as the proposed merge commit message. The
message follows Conventional Commits: use one of the types listed above, an
optional lowercase scope, and a nonempty description without surrounding
whitespace or a final period. Breaking headers, `BREAKING CHANGE` and the
historical `BREAKING-CHANGE` footer, and `DEPRECATED` footers remain valid inputs
to the release-version policy. PRLint imposes no title or body line-length limit.
Passing syntax validation does not establish that the selected type or scope
matches the change.

## Repository settings

[.github/settings.yml](../.github/settings.yml) declares labels, merge strategy,
rulesets, and Actions/security settings through gh-infra v0.13.0. CI validates
the manifest offline; it neither detects drift nor applies settings.

A maintainer reviews and applies settings locally:

```sh
gh extension install babarot/gh-infra --pin v0.13.0
gh infra plan .github/settings.yml
gh infra apply .github/settings.yml
```

Labels and rulesets reconcile authoritatively: omitted entries are deleted.
Inspect the plan before applying. Keep administrative credentials out of CI.
Settings not represented in the manifest are managed separately through GitHub.
Only squash merges are allowed. The squash commit uses the reviewed PR title
and body; the inactive merge-commit defaults retain the same values.

The required status is `pass`. The `ci-pr` commit stage builds one candidate on
each native platform; its acceptance stage verifies and tests those exact
transferred bytes without rebuilding the product. PR code and base changes move
through Draft, Commit stage, Ready, and Acceptance stage in one run. Title/body-
only edits remain isolated in PRLint and do not replace code-validation results.
The `ci-push` workflow runs the same Commit and Acceptance stages for `main`.
Fork and Dependabot pull requests that cannot change Draft/Ready state are not
supported; a maintainer must handle them manually without a privileged fallback.

Together the staged workflows preserve Go builds and tests on Linux amd64,
macOS arm64, and Windows amd64, plus race, format/vet, settings validation, and
release-preparation tests. Dependabot auto-merge uses `pull_request_target`
without checking out PR code. Required checks and resolved threads still apply.

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
accepted bytes. Each internal artifact carries a source, version, asset-name, and SHA-256
manifest; publication revalidates the complete inventory without rebuilding.
The final verification has no write token; its public API and asset downloads
are unauthenticated after read-only artifact retrieval. It compares all three
published assets with the accepted manifests. A
failed metadata/build/acceptance/publication check creates no new release.

| Commit signal | Version increment |
| --- | --- |
| Breaking header `!`, `BREAKING CHANGE` or `BREAKING-CHANGE` footer | Minor while the major version is zero; otherwise major. |
| `feat`, `perf`, or `DEPRECATED` footer | Minor. |
| `fix`, `revert`, or another substantive commit | Patch. |

The highest tier wins once for the range. Empty preparation commits do not
change the selected source and are ignored; a preparation-named commit with a
tree change remains substantive. No substantive changes, an invalid baseline,
a changed published baseline, or a conflicting tag stops publication. There is
no version override, and v0 does not automatically graduate to v1. PR labels
categorize generated release notes but do not determine the version. Tags are
not moved and published releases/assets are not replaced. Final API operations
are not atomic; inspect remote state before retrying interrupted publication.
[.github/release.yml](../.github/release.yml) owns release-note categories.
