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
Keep the squash title and body consistent with the reviewed change.

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

The required status is `CI success`, aggregating Go builds/tests on Linux
amd64/arm64, macOS arm64, Windows amd64, race, format/vet, settings validation,
and release-preparation tests. Dependabot auto-merge uses `pull_request_target`
without checking out PR code. Required checks and resolved threads still apply.
When a required context changes, land the workflow before applying its ruleset.

## Releases

Merging does not authorize releasing. Only on an explicit release request, run:

```sh
gh workflow run release.yml --ref main
```

[The workflow](../.github/workflows/release.yml) captures one main commit, uses
the latest published stable release as its baseline, classifies the complete
Conventional Commit messages in that source range, validates the captured
commit, and builds five platform binaries before creating a tag and release. A
failed preparation/build/validation creates no tag.

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
