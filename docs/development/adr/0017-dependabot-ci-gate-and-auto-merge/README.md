---
type: adr
title: "ADR-0017: Gate Dependabot merges on CI success"
description: "Use the stable CI success status to gate Dependabot auto-merge without coupling rulesets to matrix jobs."
resource: gh-qw
tags: [gh-qw, adr, adr-0017, dependabot, ci]
timestamp: 2026-08-08
---

# ADR-0017: Gate Dependabot merges on CI success

- **Status:** Accepted
- **Date:** 2026-08-08
- **Updated:** 2026-10-22
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0012](../0012-github-settings-as-code/), [ADR-0016](../0016-ubuntu-slim-runners/)

## Context

The default branch should not merge a pull request with failing required
validation. Matrix job names change when runner or platform choices evolve;
requiring those individual contexts would couple repository rules to CI
implementation details.

Before the GitHub Pages site was retired, site builds, link checks, and runtime
smoke tests were among the checks included in the required aggregate. Those
site-specific tasks have been removed together with `.pages/`.

## Decision

The stable `CI success` aggregate job requires successful Go build/test
coverage, formatting/vet, race detection, repository settings validation, and
offline release-preparation tests. The branch ruleset requires that single
context from GitHub Actions, with `strict_required_status_checks_policy: false`
to allow Dependabot pull requests to merge without repeated rebases when main
advances.

A `pull_request_target` workflow enables squash auto-merge only for Dependabot
pull requests in this repository. It never checks out or executes PR code;
GitHub performs the merge only once all required checks and rules are met.

## Consequences

### Positive

- A stable gate guards merges without depending on individual runner names.
- Passing Dependabot updates can merge automatically using the repository's
  configured squash strategy.
- The privileged auto-merge workflow does not execute untrusted PR content.

### Negative

- Non-strict checks may have been computed against an earlier main revision;
  conflicting lockfiles still require a rebase and a new CI run.
- Ruleset updates still require a reviewed local `gh infra apply`.

### Neutral

- The gate no longer builds or deploys a documentation website.
- The auto-merge policy covers all remaining Dependabot ecosystems, including
  major versions; CodeQL and labeling are not part of the required aggregate.
