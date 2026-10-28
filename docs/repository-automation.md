# Repository automation

This page owns the settings declaration, validation, review protections, and
CI workflows of the gh-qw repository itself. For gh-qw users' configuration,
see the [configuration reference](configuration.md); for contributing and
review expectations, see [Contributing](contributing.md).

## Repository settings

[.github/settings.yml](../.github/settings.yml) declares the supported
repository fields, labels, branch and tag Rulesets, Environments, Actions policies, and
private vulnerability reporting using
[gh-settings](https://github.com/daiksud/gh-settings) v0.23.0.

The [pinned validator](../.github/scripts/validate-settings.sh) downloads the
published Linux CLI, checks its fixed SHA-256 and exact version, and validates
this manifest in the Linux commit checks for PR CI, main CI, and release
builds. Direct CLI selection avoids nested composite Action ref ambiguity.
Validation is offline after installation: it neither
detects remote drift nor applies GitHub settings. These workflows need no
administrative credentials and do not mutate repository settings.

A maintainer reviews and applies settings locally with an appropriately
authorized GitHub CLI identity:

```sh
gh extension install daiksud/gh-settings --pin v0.23.0
gh settings --version # must report gh-settings v0.23.0
gh settings validate
gh settings plan
gh settings apply
```

The release pin must match the source and binary used by CI:
[gh-settings v0.23.0](https://github.com/daiksud/gh-settings/releases/tag/v0.23.0)
(source commit `cbc112ae1330b4ad4debe013857bd67d2e1a1e1e`).
Before using an administrative identity, verify the downloaded platform binary
against that immutable release's `SHA256SUMS`; a version string alone does
not authenticate the executable. If another version is already installed,
remove it with `gh extension remove gh-settings` and reinstall with the pin
above. Do not use `gh extension upgrade` for privileged settings operations:
upgrades require separately reviewing and pinning a new release.

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
gh-settings does not manage these controls. Their live settings
are **not changed** by this declaration and must be maintained separately
in GitHub. Inactive normal-merge commit-message defaults are also left
unmanaged while normal merges remain disabled.

Environments are selected by name, with all observed supported protections
declared explicitly. `github-pages` permits only the `main` branch and retains
administrator bypass; `copilot` has no branch/tag restriction and also retains
administrator bypass. Neither declaration adds reviewers or a wait timer.
Unlisted Environments are not deleted. The typed pattern list is authoritative
only for the selected custom-policy Environment; API pattern IDs are not YAML
identities. A matching complete declaration is a no-op, but verify a fresh plan
before applying because the live settings can change after observation.

Only squash merges are allowed. The squash commit uses the reviewed PR title
and body. The Actions approval setting permits the
[Codex approval workflow](../.github/workflows/approve-codex-review.yml)
to produce a valid GitHub Actions approving review when its current-HEAD
checks pass, whether the open PR is draft or ready. Copilot's reviews and Codex comments alone do not approve PRs.
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
