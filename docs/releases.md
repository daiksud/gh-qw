# Maintainer releases

This page owns version selection, manual publication, immutable assets,
validation, and retry boundaries. For ordinary development and PR validation,
see [Contributing](contributing.md).

## Releases

Release cross-builds run on `ubuntu-latest`. Native acceptance uses
`ubuntu-latest` for Linux amd64, `macos-latest` for macOS arm64, and
`windows-latest` for Windows amd64; the target OS/architecture assertions remain
in force. Published-release verification also uses `ubuntu-latest`. The resolve
and publication orchestration jobs retain `ubuntu-slim`. These `-latest` labels
follow GitHub's stable image updates rather than pinning an OS version; see the
[hosted runner specifications](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

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
no version override, and v0 does not automatically graduate to v1. Tags are
not moved and published releases/assets are not replaced. Final API operations
are not atomic; inspect remote state before retrying interrupted publication.

## Release notes

The [generator](../.github/scripts/release-notes.py) reads committed squash
messages between the previous published commit and the frozen release source.
It follows first-parent history, omits commits with the same tree as their first
parent, and lists each remaining commit once. Both boundaries are full commit
SHAs; edits to a PR after merging cannot change the generated notes.

The first matching signal determines the section:

| Commit signal | Section |
| --- | --- |
| Breaking header `!`, or a formal `BREAKING CHANGE:` / `BREAKING-CHANGE:` footer | BREAKING CHANGE |
| Formal `DEPRECATED:` footer | DEPRECATED |
| `feat` | New Feature |
| `fix` | Bug Fix |
| `perf` | Performance |
| Any other type or an unstructured message | Maintenance |

Compatibility footers must start after a blank line or another footer. Prose,
quoted text, and fenced examples are not compatibility markers. Describe the
required migration in a breaking-change footer so it appears with the change.
New Feature, Bug Fix, and Performance describe user-observable gh-qw behavior;
reviewers check that distinction before merging, following
[PR classification](contributing.md#pr-classification).

The generated file includes commit links and a comparison link pinned to both
commit SHAs. The publisher creates it after revalidating the accepted artifacts,
published baseline, and tag conflicts, and passes the file to `gh release
create`. Invalid boundaries, incomplete history, or generation failure stop
publication before that command runs.
