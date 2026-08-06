---
type: adr
title: "ADR-0010: Declare a public API and follow Semantic Versioning"
description: "Declare gh-qw's public API and adopt SemVer 2.0.0, with a documented 0.y.z convention and mechanically enforced tags."
resource: gh-qw
tags: [gh-qw, adr, adr-0010, semver, release, public-api]
timestamp: 2026-08-06
---

# ADR-0010: Declare a public API and follow Semantic Versioning

- **Status:** Accepted
- **Date:** 2026-08-06
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
command depends on (`gh` authentication, `fzf` for `list --fzf`). `stderr` wording, the `internal/`
Go package layout, documentation, CI configuration, and tests are explicitly excluded. The full,
exhaustive declaration lives in the
[versioning reference](../../../reference/versioning/#public-api), which this ADR does not
duplicate.

`gh-qw` follows SemVer 2.0.0. While the project remains `0.y.z` (clause 4), its ordinary clauses 6
through 8 still apply, but one tier down: a backward-incompatible public API change is MINOR
instead of MAJOR; a backward-compatible addition, a deprecation, or a substantial internal
improvement (including performance) is PATCH instead of MINOR; a backward-compatible bug fix
remains PATCH. `gh-qw` stays `0.y.z` under this policy even across a backward-incompatible change —
it still only bumps MINOR. Declaring `1.0.0`, and with it the point where the ordinary SemVer
clauses apply without the one-tier shift, is a deliberate, separate decision this automatic
mechanism never makes by itself.

Every tag push is verified mechanically before a release is built: a CI job computes the expected
next version from the labels of pull requests merged since the last release and fails the workflow
if the pushed tag does not match, or if no merged pull request carries a version-affecting label.
There is no override for this check. Once released, a version's contents are never modified
(SemVer clause 3); a mistaken release is corrected with a new version, never by replacing a tag,
rebuilding its assets, or rewriting its notes.

## Consequences

### Positive

- Version numbers become a mechanically checked fact about a release's contents instead of a
  judgment call made at tag time.
- Contributors have one place (the versioning reference) that says exactly what counts as a
  version-affecting change, so internal refactors, tests, and documentation never force a release.
- The `0.y.z` shift lets `gh-qw` keep tagging small, real releases (like this one) without
  prematurely declaring `1.0.0` stability the project cannot yet back.

### Negative

- Any CLI-observable change requires the correct branch prefix (see
  [ADR-0011](../0011-type-named-release-labels/)) or the release tag is rejected outright, with no
  manual escape hatch.
- The one-tier `0.y.z` shift is nonstandard SemVer and must be understood by anyone tagging a
  `gh-qw` release; it requires a deliberate decision (and likely a superseding ADR) at `1.0.0` to
  retire.

### Neutral

- This ADR declares the public API and the version-selection rule; it does not define how labels
  are computed from branch names and paths — see [ADR-0011](../0011-type-named-release-labels/).
