# Maintainer releases

This page owns version selection, manual publication, immutable assets,
validation, and retry boundaries. For ordinary development and PR validation,
see [Contributing](contributing.md).

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
