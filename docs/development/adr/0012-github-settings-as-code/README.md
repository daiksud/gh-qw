---
type: adr
title: "ADR-0012: Manage repository settings as code with local-only apply"
description: "Declare gh-qw's GitHub repository settings in .github/settings.yml via gh-infra, validating the manifest in CI while apply and drift review stay a local, manual step."
resource: gh-qw
tags: [gh-qw, adr, adr-0012, gh-infra, github-settings, ci]
timestamp: 2026-08-06
---

# ADR-0012: Manage repository settings as code with local-only apply

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0011](../0011-type-named-release-labels/)

## Context

`gh-qw`'s repository settings — its 20 labels, the branch and tag rulesets, merge strategy, and
security options — exist only as GitHub UI state. Nothing records why a setting has its current
value, no review happens before it changes, and nothing notices if it changes. This matters
concretely: [ADR-0011](../0011-type-named-release-labels/) defines a label vocabulary that must
keep matching `.github/labeler.yml` and `.github/release.yml`, and
[ADR-0010](../0010-public-api-and-semantic-versioning/)'s release process assumes the branch
ruleset keeps enforcing linear history and signed commits. A UI change to either would silently
invalidate a decision this project already made, with no record of who changed what or when.

[`gh-infra`](https://github.com/babarot/gh-infra) manages GitHub repository settings from a YAML
manifest with a `plan`/`apply` workflow and no state file of its own — GitHub is the only source of
truth `gh-infra` itself needs. Its "self-managed" pattern keeps the manifest inside the repository
it describes, changed by ordinary pull requests. Adopting it turns `gh-qw`'s settings into a
reviewable artifact instead of a set of undocumented UI clicks.

`gh-infra`'s own CI documentation states that a self-managed setup only needs the workflow's default
`GITHUB_TOKEN`. Verifying this against `gh-infra`'s source and the GitHub Actions token permission
model shows it is not true: `permissions:` in a workflow file has no `administration` key at all,
so `GITHUB_TOKEN` can never hold it, and nearly every setting `gh-qw` needs to manage — rulesets,
merge strategy, security options, Actions configuration — requires `Administration: write`.
`GITHUB_TOKEN` alone in a `gh-infra apply` step would fail on the first ruleset or merge-strategy
change with an HTTP 403. `gh-qw` is a public repository; the only credential capable of applying
these settings is a fine-grained personal access token or a GitHub App installation token with
repository administration rights, and this project accepts no standing decision to place such a
credential in this repository's CI.

Automated drift detection (a scheduled `gh infra plan`) was prototyped and made to work end to end
during this change's development, but it needed a dedicated GitHub App, a read-only installation
token minted per run, a preflight check, and a two-layer guard against a silent-failure mode in
`gh infra plan --ci` itself (see the Decision below for what that mode is). That standing
credential, its permissions, and its private key are an ongoing operational cost this project is
not willing to carry merely to catch drift automatically once a month, when a maintainer can run
`gh infra plan` locally in seconds whenever they suspect it. The prototype is not adopted.

## Decision

Repository settings are declared in `.github/settings.yml`, a single `gh-infra` `Repository`
manifest covering the description, labels, merge strategy, security options, rulesets, and Actions
configuration already in effect (`gh infra import` was used to seed it, so adopting it changes
nothing on GitHub by itself). `reconcile.labels` and `reconcile.rulesets` are both `authoritative`:
removing an entry from the manifest deletes it on GitHub, not just stops tracking it, so the
manifest is a complete description rather than a partial overlay.

`.github/settings.yml` was chosen over `gh-infra`'s own documented convention of
`.github/infra.yaml` because the filename `settings.yml` is independently recognized across GitHub
from the older Probot "Settings" App, and using the more widely recognized name aids a contributor
already familiar with that convention from other repositories. No Probot Settings App is installed
on `daiksud/gh-qw`, so there is no present schema conflict; this trades a filename that is
`gh-infra`-idiomatic for one that is cross-project-idiomatic, and accepts that installing the Probot
App here later would need to be reconciled with this choice.

**Applying a settings change is a local, manual `gh infra apply .github/settings.yml`, run by a
maintainer — never CI.** This follows directly from the token finding above: since applying most of
this manifest requires `Administration: write`, and this project will not place a credential with
that scope in a public repository's CI, CI structurally cannot apply settings at all.

**CI runs exactly one check: `gh infra validate .github/settings.yml`**, a job in the existing
`.github/workflows/ci.yml` alongside the Go and documentation checks. `validate` parses and
schema-checks the manifest; it makes no GitHub API call, needs no credential at all, and therefore
also runs unmodified on pull requests from forks. It catches a malformed manifest before merge. It
does **not** detect drift between the manifest and live GitHub state — that comparison
(`gh infra plan`) is left to a maintainer to run locally, typically while reviewing a settings pull
request or investigating a suspected out-of-band change, immediately before `apply`.

This is a deliberate reduction from an earlier design tried during this change's development, where
CI additionally ran `gh infra plan` — on pull requests to surface the diff, and with `--ci` on a
monthly schedule and manual dispatch to catch drift automatically. That design was fully built and
verified working end to end against this repository's live state, but needed a GitHub App
installation token granted `Administration: read`, `Contents: read`, `Issues: read`,
`Metadata: read`, `Secrets: read`, and `Variables: read` — the last two only because `gh infra plan`
was found, empirically, to unconditionally list a repository's secret and variable names while
building its full state view, regardless of what the manifest itself declares. Standing up and
maintaining that App — its permissions, its private key, its rotation — for a monthly drift check
was judged not worth the
operational cost relative to a maintainer simply running `gh infra plan` by hand when they want to
know. Two findings from that abandoned design are worth preserving for whoever revisits automated
drift detection later:

- **`gh infra plan --ci` exits `0` even when every API call failed to authenticate** — verified
  empirically. A drift-detection job that trusts this exit code alone can silently report "no
  drift" while actually unable to read anything; guard it by also checking `plan`'s own output for
  repositories skipped due to errors.
- **A read-only (`Administration: read`) credential cannot see five `merge_strategy` fields at
  all** — `allow_auto_merge` and the four commit-message-template fields
  (`squash_merge_commit_title`/`_message`, `merge_commit_title`/`_message`) come back `null` from
  `GET /repos/{owner}/{repo}` for such a credential, a permission-gated field-visibility behavior in
  GitHub's own API rather than a `gh-infra` bug. Declaring them in the manifest while reading with a
  read-only token produces permanent, unfixable false drift. This is not a concern for the design
  adopted here, since `apply`/`plan` both run locally with a maintainer's own, fully-privileged
  credential — the five fields are declared in `spec.merge_strategy` normally.

The full operating model and the settings `gh-infra` cannot manage at all (GitHub Pages,
Environments, the CodeQL default setup, webhooks, and others) are documented in the
[repository settings reference](../../../reference/repository-settings/) rather than repeated here.

## Consequences

### Positive

- A repository setting change now goes through the same pull request review as a code change,
  instead of an unrecorded UI click.
- CI never holds any GitHub credential capable of reading or writing repository settings —
  `validate` makes no API call at all — so there is no standing credential in this repository to
  misconfigure, rotate, or ever escalate from.
- Adopting the manifest changed nothing on GitHub: `gh infra plan`, run locally, reported no
  differences before adding the `authoritative` reconcile policy and after.
- The manifest covers every field `gh-infra` supports, including the five `merge_strategy` fields
  a CI-held read-only credential could never have verified — nothing is left unmanaged to work
  around a credential this design does not have.

### Negative

- There is no automated drift detection: an out-of-band change made through the GitHub UI is
  invisible until a maintainer happens to run `gh infra plan` locally. This is an accepted,
  deliberate trade-off against the operational cost of a standing CI credential — see Context and
  Decision.
- `.github/settings.yml` is deliberately not `gh-infra`'s own idiomatic filename; if a Probot
  Settings App were ever installed on this repository, its interpretation of the same file could
  conflict with `gh-infra`'s.
- `gh-infra` treats unknown manifest keys as hard errors, so the CI extension install is pinned to
  `v0.13.0`; upgrading `gh-infra` and the manifest schema together needs a deliberate, coordinated
  change rather than happening automatically.
- `authoritative` reconcile makes deleting a manifest entry a real deletion: removing a label
  strips it from every issue and pull request currently carrying it. `gh infra plan` must be
  reviewed before every `apply`.

### Neutral

- Settings `gh-infra` has no field for — GitHub Pages, Environments, the CodeQL default setup,
  webhooks, collaborators, and the default branch name among them — remain manually managed exactly
  as before. They are enumerated in the
  [repository settings reference](../../../reference/repository-settings/), not here.
- The operating workflow (edit → PR → review a locally-run `gh infra plan` diff → merge → local
  apply) and the label-color rationale now live in the
  [repository settings reference](../../../reference/repository-settings/) and
  [versioning reference](../../../reference/versioning/) rather than being duplicated in this ADR.
- A future maintainer who wants automated drift detection back has a working starting point: a
  read-only GitHub App plus a `plan --ci` job, guarded against the two findings recorded in the
  Decision above.

