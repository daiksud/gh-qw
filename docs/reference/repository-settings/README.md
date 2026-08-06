---
type: reference
title: "Repository settings reference"
description: "Normative contract for gh-qw's GitHub repository settings manifest, its apply/verify workflow, required token permissions, and what remains unmanaged."
resource: gh-qw
tags: [gh-qw, reference, github-settings, gh-infra, ci]
timestamp: 2026-08-06
---

# Repository settings reference

`gh-qw`'s GitHub repository settings — labels, merge strategy, security options, branch and tag
rulesets, and Actions configuration — are declared in [`.github/settings.yml`](https://github.com/daiksud/gh-qw/blob/main/.github/settings.yml),
a [`gh-infra`](https://github.com/babarot/gh-infra) `Repository` manifest. See
[ADR-0012](../../development/adr/0012-github-settings-as-code/) for why settings are managed this
way and why applying them stays a local, manual step. The [versioning reference](../versioning/)
defines which labels affect release version computation; this page defines the manifest itself.

## Workflow

1. Edit `.github/settings.yml` and open a pull request.
2. `.github/workflows/infra.yml` runs `gh infra validate` (always) and `gh infra plan` (on
   same-repository pull requests) automatically, posting the plan diff to the job summary.
   Review that diff like any other code change.
3. After merge, a maintainer applies the change from a local machine:

   ```console
   $ gh extension install babarot/gh-infra --pin v0.13.0
   $ gh infra plan .github/settings.yml
   $ gh infra apply .github/settings.yml
   ```

   Always pass the file path, not the `.github/` directory — `gh-infra` silently skips YAML files
   that are not its own manifests (`labeler.yml`, workflow files) when given a directory, but
   passing the file explicitly avoids depending on that behavior.

`apply` is never run in CI. See [Token permissions](#token-permissions) for why.

## Authoritative reconcile

`.github/settings.yml` sets `reconcile.labels: authoritative` and `reconcile.rulesets:
authoritative`. Under the default (`additive`) policy, `gh-infra` only creates and updates entries;
under `authoritative`, an entry present on GitHub but absent from the manifest is **deleted** on the
next `apply`. Removing a label from the manifest removes it from every issue and pull request that
currently carries it. Always inspect `gh infra plan`'s output before running `apply`.

## Token permissions

Nearly every setting in this manifest requires `Administration: write` — merge strategy, rulesets,
security options, and Actions configuration all do. The default `GITHUB_TOKEN` GitHub Actions
provides can never hold that permission: `administration` is not one of the keys a workflow's
`permissions:` block accepts. This is true even for a single, self-managed repository — see
ADR-0012 for how this was verified against `gh-infra`'s source. Consequently:

| Operation | Required permission | `GITHUB_TOKEN`? |
| --- | --- | --- |
| Read settings (`gh infra plan`) | `Administration: read`, `Contents: read`, `Issues: read`, `Metadata: read` | No — none of these reads are available to `GITHUB_TOKEN` for `Administration` |
| Write labels / milestones | `Issues: write` | Yes |
| Write description / features / merge strategy / topics / visibility / security / rulesets / Actions config | `Administration: write` | No |
| Write secrets / variables | `Secrets: write` / `Variables: write` | No |

`gh-qw` does not declare `spec.secrets` or `spec.variables` in its manifest, so those rows are shown
for completeness only.

`.github/workflows/infra.yml` therefore uses two different credentials:

- **`validate`** makes no GitHub API call at all and needs no credential. It runs unmodified on
  pull requests from forks, where secrets are never available.
- **`plan`** authenticates with a GitHub App installation token scoped to **`Administration: read`,
  `Contents: read`, `Issues: read`, `Metadata: read` only** — enough to detect drift in everything
  the manifest manages, and structurally incapable of writing any of it. The App must be installed
  on `daiksud/gh-qw` with exactly those four permissions as read-only, with its ID and private key
  stored as the `GH_INFRA_APP_ID` and `GH_INFRA_APP_PRIVATE_KEY` repository secrets. The workflow
  additionally requests the same four permissions explicitly on the minted token
  (`permission-administration: read`, etc.); requesting a permission the App installation lacks
  fails the token-minting step outright, so a misconfigured App is caught immediately rather than
  producing an under-scoped token silently.

No credential in this repository's CI can apply a settings change. `apply` requires a fine-grained
personal access token or GitHub App installation token with `Administration: write`, deliberately
never stored here.

## Drift detection

`plan` runs on same-repository pull requests (diff shown for review, not treated as failure — a
settings pull request is expected to show one), and with `--ci` on a monthly schedule and on manual
`workflow_dispatch`, where any diff means the live state has drifted from the manifest outside of a
reviewed pull request.

`gh infra plan --ci` was found to exit `0` even when every API call failed to authenticate, so a
stale or misconfigured App credential could otherwise be silently reported as "no drift". The
`plan` job guards against this in two ways: a preflight read of `actions/permissions` with the same
token before calling `plan` at all, and a check of `plan`'s own output for repositories skipped due
to errors, which fails the job regardless of `plan`'s exit code.

Scheduled workflows on public repositories are disabled automatically after 60 days without
repository activity; an inactive `gh-qw` would need a commit, merge, or manual `workflow_dispatch`
run to re-enable the monthly schedule.

## Unmanaged settings

`gh-infra` v0.13.0 has no field for the following, and they remain manually managed through the
GitHub UI or API exactly as before this manifest existed:

- **GitHub Pages** — `gh-qw`'s Pages site (`build_type: workflow`) has no corresponding manifest
  field.
- **Environments** — including the `github-pages` environment Pages deployment creates; `gh-infra`
  has no `Environment` kind and cannot manage environment protection rules, reviewers, or secrets.
- **CodeQL default setup** — configured via the UI (`languages: [actions, go]`,
  `query_suite: extended`), with no corresponding workflow file and no manifest field.
- Webhooks, collaborators and team permissions, deploy keys, and the default branch name.
- Artifact/log retention, cache limits, and OIDC subject claim customization.

## Related documents

- [ADR-0012](../../development/adr/0012-github-settings-as-code/) — why settings are code, and why
  apply stays local.
- [ADR-0011](../../development/adr/0011-type-named-release-labels/) — the label vocabulary this
  manifest's `spec.labels` implements.
- [Versioning reference](../versioning/#labels) — which labels affect the computed release version.
- [Contributing](../../development/contributing/) — the day-to-day change workflow.
