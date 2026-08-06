---
type: adr
title: "ADR-0012: Manage repository settings as code with local-only apply"
description: "Declare gh-qw's GitHub repository settings in .github/settings.yml via gh-infra, applying only from a local machine while CI verifies with a read-only token."
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

## Decision

Repository settings are declared in `.github/settings.yml`, a single `gh-infra` `Repository`
manifest covering the description, labels, merge strategy, security options, rulesets, and Actions
configuration already in effect (`gh infra import` was used to seed it with the exact live state, so
adopting it changes nothing on GitHub by itself). `reconcile.labels` and `reconcile.rulesets` are
both `authoritative`: removing an entry from the manifest deletes it on GitHub, not just stops
tracking it, so the manifest is a complete description rather than a partial overlay.

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

CI instead runs two checks, both defined in `.github/workflows/infra.yml`:

- **`validate`** parses and schema-checks the manifest. It makes no GitHub API call, needs no
  credential, and therefore also runs unmodified on pull requests from forks.
- **`plan`** reports drift between the manifest and live GitHub state, authenticating with a GitHub
  App installation token. The App must be granted exactly `Administration: read`, `Contents: read`,
  `Issues: read`, `Metadata: read`, `Secrets: read`, and `Variables: read` — enough to read every
  setting `gh-infra` manages, and structurally incapable of writing any of them. The last two are
  required even though this manifest declares no `spec.secrets`/`spec.variables`: `gh infra plan`
  was found, empirically, to unconditionally list a repository's secret and variable names while
  building its full state view, regardless of what the manifest itself declares; both permissions
  expose names and metadata only; GitHub never returns secret or variable values through this or
  any API. The workflow does not narrow the minted token to an explicit permission subset the way
  it initially did: `actions/create-github-app-token@v3.2.0` has no input for the `Variables`
  permission at all, and requesting any explicit subset excludes everything unlisted regardless of
  what the installation actually grants, so a partial narrowing (five requested permissions plus
  whatever Variables access the App happens to have) is not expressible with this action. The App's
  own installation permissions are therefore the only boundary on this token, which is why they
  must be exactly those six, read-only, with nothing else granted. It runs on same-repository pull
  requests (surfacing the diff for review, without `--ci`, since a settings pull request is expected
  to show one), and with `--ci` on a monthly schedule and on manual dispatch, where any diff means
  unreviewed drift. Verifying `gh infra plan --ci`'s actual behavior found it exits `0` even when
  every API call failed to authenticate, so a stale or misconfigured App credential could be
  silently reported as "no drift"; the `plan` job therefore also runs a preflight API read with the
  same token before calling `plan`, and separately fails if `plan`'s own output reports repositories
  skipped due to errors, regardless of `plan`'s exit code. This preflight guard was itself confirmed
  live, twice: an App granted only four of the six permissions made `plan` fail to list secrets with
  an HTTP 403, and, after adding `Secrets: read`, an App still missing `Variables: read` made it fail
  to list variables the same way — both times the job correctly failed instead of reporting false
  "no drift".

The full operating model, the token/permission table, and the settings `gh-infra` cannot manage at
all (GitHub Pages, Environments, the CodeQL default setup, webhooks, and others) are documented in
the [repository settings reference](../../../reference/repository-settings/) rather than repeated
here.

## Consequences

### Positive

- A repository setting change now goes through the same pull request review as a code change,
  instead of an unrecorded UI click.
- The CI credential is read-only by construction (both by the App's own installation permissions
  and by the narrower `permission-*` grant requested for each token), so CI cannot mutate live
  settings even if a workflow were compromised — there is no credential to escalate.
- Drift between the manifest and live GitHub state is caught automatically at least monthly, and
  immediately on demand via manual dispatch, rather than only when someone happens to notice.
- Adopting the manifest changed nothing on GitHub: `gh infra plan` reported no differences both
  before and after adding the `authoritative` reconcile policy.

### Negative

- A maintainer must create and maintain a GitHub App and rotate its private key outside of any
  code in this repository; nothing here automates that prerequisite.
- Detection lag is real: an out-of-band UI change is caught within a month by schedule, not
  instantly — only a manual dispatch run or the next scheduled run reveals it.
- `.github/settings.yml` is deliberately not `gh-infra`'s own idiomatic filename; if a Probot
  Settings App were ever installed on this repository, its interpretation of the same file could
  conflict with `gh-infra`'s.
- `gh-infra` treats unknown manifest keys as hard errors, so the CI extension install is pinned to
  `v0.13.0`; upgrading `gh-infra` and the manifest schema together needs a deliberate, coordinated
  change rather than happening automatically.
- `authoritative` reconcile makes deleting a manifest entry a real deletion: removing a label
  strips it from every issue and pull request currently carrying it. `gh infra plan` must be
  reviewed before every `apply`.
- The `plan` job's token carries no App-side narrowing to an explicit permission subset, since
  `actions/create-github-app-token@v3.2.0` cannot express the `Variables` permission and any
  narrowing would otherwise silently exclude it. The App's own installation permissions are the
  only thing keeping this token to exactly six read scopes; a future accidental grant of a seventh
  permission, or of write access on any of the six, would flow into the token unnoticed by anything
  in this repository, rather than being caught immediately the way an over-broad request would have
  been with explicit narrowing.

### Neutral

- Settings `gh-infra` has no field for — GitHub Pages, Environments, the CodeQL default setup,
  webhooks, collaborators, and the default branch name among them — remain manually managed exactly
  as before. They are enumerated in the
  [repository settings reference](../../../reference/repository-settings/), not here.
- The exhaustive token/permission table, the operating workflow (edit → PR → review the plan diff →
  merge → local apply), and the label-color rationale now live in the
  [repository settings reference](../../../reference/repository-settings/) and
  [versioning reference](../../../reference/versioning/) rather than being duplicated in this ADR.
