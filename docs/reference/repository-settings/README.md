---
type: reference
title: "Repository settings reference"
description: "Normative contract for gh-qw's GitHub repository settings manifest, its local-only apply workflow, required token permissions, and what remains unmanaged."
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
2. `.github/workflows/ci.yml`'s `settings` job runs `gh infra validate .github/settings.yml`. It is
   fully offline and only catches a malformed manifest — it does not compare against live GitHub
   state. Review the diff itself like any other code change.
3. Before or after merge, a maintainer checks for drift and applies the change from a local
   machine:

   ```console
   $ gh extension install babarot/gh-infra --pin v0.13.0
   $ gh infra plan .github/settings.yml
   $ gh infra apply .github/settings.yml
   ```

   Always pass the file path, not the `.github/` directory — `gh-infra` silently skips YAML files
   that are not its own manifests (`labeler.yml`, workflow files) when given a directory, but
   passing the file explicitly avoids depending on that behavior.

Neither `plan` nor `apply` ever runs in CI. See [Token permissions](#token-permissions) for why.

## Required checks and Dependabot auto-merge

The `docs` job in [`.github/workflows/ci.yml`](https://github.com/daiksud/gh-qw/blob/main/.github/workflows/ci.yml)
validates the documentation source, builds the Astro site, checks generated links, and runs the
runtime smoke test. The `CI success` job aggregates that result with the Go and settings jobs so
the ruleset does not depend on matrix runner names.

The default branch ruleset requires the `CI success` status from the `github-actions` app. The
required check uses `strict_required_status_checks_policy: false` intentionally: GitHub auto-merge
does not update a pull request head branch, and Dependabot does not rebase solely because the base
branch advanced. Requiring an up-to-date branch would therefore leave queued Dependabot pull
requests behind after another update merged.

`.github/workflows/dependabot-auto-merge.yml` enables squash auto-merge for every Dependabot pull
request after it is opened or updated. It uses `pull_request_target` without checking out the
head branch, and the required `CI success` check still prevents a failing update from merging.

When introducing or renaming a required status context, merge the workflow change first. Then
review and apply the manifest locally:

```console
$ gh infra plan .github/settings.yml
$ gh infra apply .github/settings.yml
```

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
| Read settings (`gh infra plan`) | `Administration: read` (plus `Contents`/`Issues`/`Metadata`/`Secrets`/`Variables`: read) | No |
| Write labels / milestones | `Issues: write` | Yes |
| Write description / features / merge strategy / topics / visibility / security / rulesets / Actions config | `Administration: write` | No |
| Write secrets / variables | `Secrets: write` / `Variables: write` | No |

`.github/workflows/ci.yml`'s `settings` job needs **no credential at all**: `gh infra validate`
makes no GitHub API call, so it runs unmodified on pull requests from forks, where secrets are
never available anyway.

No credential in this repository's CI can read or write a repository setting. Both `gh infra plan`
and `gh infra apply` require a fine-grained personal access token or a GitHub App installation
token — `Administration: write` for `apply`, at least `Administration: read` for `plan` — held only
on a maintainer's own machine, never in this repository's CI.

## Drift detection

There is no automated drift detection. CI's `settings` job only validates the manifest's syntax and
schema — it never compares against live GitHub state. If GitHub's UI is used to change a setting
this manifest declares, nothing here will notice.

A maintainer who wants to check for drift — for example, while reviewing a settings pull request,
or after suspecting an out-of-band UI change — runs `gh infra plan .github/settings.yml` locally.
This is a deliberate trade-off: an earlier design ran `gh infra plan` automatically in CI (on pull
requests and on a monthly schedule) using a read-only GitHub App installation token, and that design
worked, but maintaining the App's permissions and private key was judged not worth the operational
cost relative to running `plan` by hand. See [ADR-0012](../../development/adr/0012-github-settings-as-code/)
for the full reasoning and for two pitfalls worth knowing if automated drift detection is ever
revisited: `gh infra plan --ci` exits `0` even when every API call failed to authenticate, and a
read-only credential cannot see certain `merge_strategy` fields at all (GitHub's API returns `null`
for them). Neither applies to this design, since `plan`/`apply` here always run with a maintainer's
own, fully-privileged local credential.

## Unmanaged settings

`gh-infra` v0.13.0 has no field for the following, and they remain manually managed through the
GitHub UI or API exactly as before this manifest existed:

- **GitHub Pages** — `gh-qw`'s Pages site (`build_type: workflow`) has no corresponding manifest
  field. The site is published at
  [https://daiksud.github.io/gh-qw/](https://daiksud.github.io/gh-qw/) by
  `.github/workflows/pages.yml` after changes to `docs/` or `.pages/` reach `main`; the workflow
  can also be dispatched manually.
- **Environments** — including the `github-pages` environment Pages deployment creates; `gh-infra`
  has no `Environment` kind and cannot manage environment protection rules, reviewers, or secrets.
- **CodeQL default setup** — configured via the UI (`languages: [actions, go]`,
  `query_suite: extended`), with no corresponding workflow file and no manifest field.
- **Pull request branch update suggestions** — the `allow_update_branch` repository setting is not
  represented by gh-infra v0.13.0 and remains manually managed through the GitHub UI or API.
- Webhooks, collaborators and team permissions, deploy keys, and the default branch name.
- Artifact/log retention, cache limits, and OIDC subject claim customization.

## Related documents

- [ADR-0012](../../development/adr/0012-github-settings-as-code/) — why settings are code, and why
  apply stays local.
- [ADR-0017](../../development/adr/0017-dependabot-ci-gate-and-auto-merge/) — how Dependabot
  merges are gated and automated.
- [ADR-0011](../../development/adr/0011-type-named-release-labels/) — the label vocabulary this
  manifest's `spec.labels` implements.
- [Versioning reference](../versioning/#labels) — which labels affect the computed release version.
- [Contributing](../../development/contributing/) — the day-to-day change workflow.
