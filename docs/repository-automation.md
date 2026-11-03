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
or Ruleset deletion is implied. For label-only maintenance, use an isolated
checkout of this repository with a `.github/settings.yml` containing only
`schemaVersion: 1` and the `settings.labels` declaration. Validate it with the
verified v0.23.0 CLI and inspect its plan before applying. This prevents unrelated
settings drift from changing protections, permissions, or security controls.
Record confirmed new IDs in the maintained manifest, then plan again; a successful
apply alone does not establish that a second plan has no differences. There is
no automatic settings-apply workflow in this repository.

The pinned CLI validates the manifest offline, but a full live plan currently
fails on the main Ruleset's `code_scanning_tools` parameter, which v0.23.0 does
not support. A full plan therefore cannot establish drift for this repository.
Keep label maintenance isolated; managing the complete settings declaration
requires a separate review of CLI and schema support.

The existing Rulesets keep required signed
commits, linear history, CODEOWNER review, one eligible approval, stale review
dismissal, review-thread resolution, and the `ci-pr` and `prlint` GitHub
Actions checks. Enforcement remains subject to the live bypass configuration.
The tag Ruleset continues preventing
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
to submit a GitHub Actions approving review when its Codex review and HEAD conditions
are met, whether the PR is draft or ready. Copilot's reviews and Codex comments alone do not approve PRs.
### Labels

The managed set is `accessibility`, `bug`, `documentation`, `duplicate`,
`enhancement`, `good first issue`, `help wanted`, `invalid`, `question`, and
`wontfix`. Other labels and their existing Issue/PR assignments coexist and
remain unmanaged. Removing a label from the declaration does not delete it.

Bug and feature Issue forms assign `bug` and `enhancement`; maintenance tasks
have no initial label. The [Labeler](../.github/workflows/labeler.yml) synchronizes
only `documentation`, using the trusted base branch's
[path rules](../.github/labeler.yml). It adds that label when every changed file
is documentation and removes it when the rule stops matching. It does not
manage other labels. Dependabot uses its default labels without an explicit
`labels` override. Reviewers choose Conventional Commit types independently;
labels do not determine release versions or release-note categories.

Labeler is pinned to the untagged v7.0.0 follow-up
[selective synchronization fix](https://github.com/actions/labeler/pull/957).
It adds matching labels and removes stale configured labels individually,
preserving concurrent changes to unrelated labels instead of replacing the
entire PR label set. This fix is not included in the v7.0.0 tag.

### Review and merge responsibilities

Rulesets decide merge eligibility: required approvals, stale approval dismissal,
required checks and synchronization with the base branch belong to GitHub's
protection rules. The live `default` [Ruleset](https://github.com/daiksud/gh-qw/rules/20506385),
read on 2026-11-01, was active for `main`, with
`dismiss_stale_reviews_on_push: true` and
`strict_required_status_checks_policy: true` for `ci-pr` and `prlint`.
Recheck live settings when making a merge decision; repository files alone do
not establish enforcement, and applicable bypass permissions still matter.

GitHub's [stale approval rule](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches#require-pull-request-reviews-before-merging)
tracks changes to the approved diff, including changes introduced through the
merge base. A new HEAD SHA or an update to `main` is not by itself a complete
statement of the dismissal conditions. [Strict required checks](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets#require-status-checks-to-pass-before-merging)
require an up-to-date base before merge. Neither rule proves that Codex reviewed
a particular base.

The [approval workflow](../.github/workflows/approve-codex-review.yml) follows
[gh-settings #554](https://github.com/daiksud/gh-settings/pull/554):

1. Codex has a thumbs-up reaction on the PR.
2. The latest Codex Review Summary shows both Code Review and Security Review
   as Completed.
3. Both full reviewed commit SHAs match the current PR HEAD.
4. This PR has no `github-actions[bot]` review in `APPROVED` or `DISMISSED`
   state for that full SHA.

It identifies Codex through GitHub's `user.login`, selects the latest Summary
comment by ID, and reads the two status cells in its Markdown table. After the
signals are present, it reads PR HEAD once and compares it with both full
`commit_sha` fields in `codex-review-state:v1`. Missing, malformed or ambiguous
commit metadata withholds approval. It submits that verified SHA as `commit_id`
and requires GitHub to confirm approval of the same commit.

Summary creation/edits and completed `ci-pr` runs trigger evaluation. The CI
run's conclusion is not an approval condition. Because Summary updates can
precede the reaction, the workflow waits for thumbs-up with up to six 30-second
retries. This bound is operational, not evidence of review completion. Each
attempt reads Summary and reaction once; HEAD is read only after both signals
are present. It does not reread the signals immediately before submission.
A reaction alone does not trigger this workflow; after the polling window,
another Summary or `ci-pr` completion event is needed. Runs for a PR are
serialized. Before POST, the workflow reads all pages of this PR's review
history once. Existing automatic approvals count without a body marker; a
dismissed approval is never restored for the same HEAD, even after another
Codex review. Another HEAD, PR or reviewer does not share that history.
History-read failures fail the run without approving. The history read and
POST are separate operations, not an atomic reservation against other writers.

Base, timestamps, receipts, finding flags, earlier requests and requested
changes are not additional approval conditions. A completed same-HEAD Summary
and existing thumbs-up remain reusable after a base change when this HEAD has
no automatic approval history, so approval does not establish which diff Codex
reviewed. Verification and POST are separate
operations: a later push does not change the submitted `commit_id`, but this is
not an atomic current-HEAD guarantee. The workflow neither decides merge
eligibility nor withdraws approvals. Rulesets enforce their configured
approvals, stale dismissal, required checks and thread resolution. A successful
run can also report approval withheld; only GitHub's confirmed approving review
is an approval.

### CI validation

A successful validation job does not prove the manifest has been applied;
verify the live Ruleset and settings separately before claiming enforcement.

Required PR checks are `ci-pr` and `prlint`, provided by GitHub Actions.
The `ci-pr` commit stage builds one candidate on each native platform; its
acceptance stage verifies and tests those exact transferred bytes without
rebuilding the product. PR code and base changes move through Draft, Commit
stage, Ready, and Acceptance stage in one run. Title/body-only edits rerun
PRLint without changing Draft/Ready state or executing product stages; their
separate concurrency group cannot cancel running or queued product validation.
The skipped final job has a different check name, leaving the required `ci-pr`
result from real validation intact. Dependabot-authored pull requests skip the
PRLint job; GitHub treats the skipped check as successful for branch protection.
Other required checks and review protections remain unchanged.
The native `ci-pr` aggregate uses `always()` and requires every prerequisite
to succeed, including after cancellation; it has no write token. The
`ci-push` workflow runs the same Commit and Acceptance stages for `main`
and finishes with its own `ci-push` aggregate job.
Fork and Dependabot pull requests that cannot change Draft/Ready state are not
supported; a maintainer must handle them manually without a privileged fallback.

Product CI builds and accepts Linux amd64 on `ubuntu-latest`, macOS arm64 on
`macos-latest`, and Windows amd64 on `windows-latest`. Target metadata retains
the OS/architecture checks, so an unexpected host architecture fails rather
than changing the artifact target. Lightweight orchestration jobs continue
using `ubuntu-slim`. The `-latest` labels follow GitHub's stable image updates;
see the [hosted runner specifications](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

Together the staged workflows preserve Go builds and tests on Linux amd64,
macOS arm64, and Windows amd64, plus race, format/vet, settings validation, and
release-version tests. Dependabot checks Go modules and GitHub Actions weekly.
Its PRs require a manual merge decision after review and required checks; no
workflow automatically enables auto-merge. The repository-level auto-merge
option remains available.
