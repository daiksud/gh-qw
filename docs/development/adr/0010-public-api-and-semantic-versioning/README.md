---
type: adr
title: "ADR-0010: Declare a public API and follow Semantic Versioning"
description: "Declare gh-qw's public API and adopt SemVer 2.0.0, with a 0.y.z policy and release validation before tagging."
resource: gh-qw
tags: [gh-qw, adr, adr-0010, semver, release, public-api]
timestamp: 2026-08-09
---

# ADR-0010: Declare a public API and follow Semantic Versioning

- **Status:** Accepted
- **Date:** 2026-08-09
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0008](../0008-conventional-commits-and-release-notes/), [ADR-0011](../0011-type-named-release-labels/)

## Context

`gh-qw` has tagged releases since `v0.1.0`, but never declared what its public API is, nor which
[Semantic Versioning 2.0.0](https://semver.org/) clause governs a version bump while the project is
still `0.y.z`. SemVer clause 1 requires a declared public API before version numbers can carry
meaning at all; without one, a release's version number is an arbitrary choice instead of a
verifiable fact about the change it ships.

`gh-qw` is also distributed only as prebuilt binaries (see
[ADR-0003](../0003-go-cobra-prebuilt-binaries/)), so its Go source is not itself a consumed API —
the public API is entirely observable CLI behavior.

## Decision

The public API is the CLI-observable contract: command and subcommand names and aliases; flag
names, short forms, meaning, and defaults; positional-argument and repository-specification syntax;
the `stdout` output contract; process exit statuses; configuration keys, environment variables, and
their precedence; the on-disk layout and identity forms; and the external-tool prerequisites a
command depends on (such as `gh` authentication). `stderr` wording, the `internal/`
Go package layout, documentation, CI configuration, and tests are explicitly excluded. The full,
exhaustive declaration lives in the
[versioning reference](../../../reference/versioning/#public-api), which this ADR does not
duplicate.

`gh-qw` follows SemVer 2.0.0. While the project remains `0.y.z` (clause 4), its ordinary MINOR and
PATCH rules apply: a backward-compatible addition, a deprecation, or a substantial internal
improvement (including performance) is MINOR, and a backward-compatible bug fix remains PATCH.
The sole `0.y.z` exception is that a backward-incompatible public API change is MINOR instead of
MAJOR, keeping the major version at zero. The next release is computed from the reviewed labels of PRs merged after
the latest published stable release. A maintainer manually dispatches the
Release workflow from `main`; branch names have no part in versioning.
Authors and reviewers may correct PR titles and labels without renaming
branches.

The workflow captures one commit and runs full CI and every binary
build against it before tag or GitHub Release creation. Tag pushes do not
trigger releases. Graduation from `0.y.z` to `1.0.0` remains a deliberate
policy change.

Published versions remain immutable (SemVer clause 3). A preparation,
test, or build failure leaves no tag. Final GitHub API operations are
not atomic and may leave a tag or draft if they fail.

## Consequences

### Positive

- Version selection is mechanical but its semantic inputs are reviewed.
- PR type and labels may be corrected without renaming a branch.
- All release tests and binaries succeed before tag creation.
- Failed/unpublished tags cannot change the release baseline.
- A selected commit remains fixed if `main` advances.

### Negative

- Reviewers must detect missing or incorrect semantic labels.
- Each release reruns CI as well as its binary builds.
- An API failure in the last step can leave a tag or draft release.

### Neutral

- Breaking changes select MINOR during `0.y.z` as declared above.
- [ADR-0011](../0011-type-named-release-labels/) defines label ownership.
