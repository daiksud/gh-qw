---
type: adr
title: "ADR-0017: Gate Dependabot merges on site validation"
description: "Require the documentation site CI gate before Dependabot pull requests can merge and enable safe automatic squash merges after the gate passes."
resource: gh-qw
tags: [gh-qw, adr, adr-0017, dependabot, ci, astro]
timestamp: 2026-08-08
---

# ADR-0017: Gate Dependabot merges on site validation

- **Status:** Accepted
- **Date:** 2026-08-08
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0012](../0012-github-settings-as-code/), [ADR-0013](../0013-node-pnpm-viteplus-toolchain/), [ADR-0016](../0016-ubuntu-slim-runners/)

## Context

Dependabot updates the documentation site's dependencies in `.pages/package.json` and
`.pages/pnpm-lock.yaml`. The existing CI validation builds the Astro site, checks its source, and
checks generated links, but the repository ruleset did not require any of those checks before a
pull request could merge. The repository also allowed auto-merge without a workflow that enabled
it for Dependabot pull requests.

Individual matrix job names are coupled to runner labels. Requiring those names directly would
make a runner change create a stale required status context. A runtime check is also useful
because a successful static build does not prove that the generated site can serve its routes and
assets over HTTP.

## Decision

The documentation validation task includes an Astro preview smoke test. The test serves the
generated `dist` directory and verifies every generated HTML route, first-party homepage assets,
the Pagefind script, and a not-found response.

CI exposes a stable `CI success` aggregate job that depends on the existing lint, test, race,
documentation, and settings jobs. The default branch ruleset requires that context from the
GitHub Actions app. The required status check does not require the head branch to be up to date:
`strict_required_status_checks_policy` remains `false` so multiple Dependabot pull requests can
drain through auto-merge without waiting for Dependabot to rebase behind branches.

A `pull_request_target` workflow enables squash auto-merge for every Dependabot pull request.
It never checks out or executes pull request code and only runs for this repository. GitHub
merges the pull request only after the required `CI success` check and all other repository
requirements pass.

## Consequences

### Positive

- A failed Astro build, source check, link check, or runtime smoke test blocks every merge.
- The required status context is independent of the operating-system and runner matrix.
- Passing Dependabot updates are merged automatically with the repository's configured squash
  strategy.
- The privileged auto-merge workflow does not execute untrusted pull request code.

### Negative

- The smoke test adds a local HTTP server and requests to the documentation validation job.
- The non-strict status policy permits a pull request to be validated against a slightly older
  base branch; shared dependency lockfile conflicts still require a rebase and another CI run.
- Ruleset changes remain a local, manual `gh infra apply` operation.

### Neutral

- The auto-merge workflow applies to all Dependabot ecosystems, including major updates; dependency
  scope is not used as an implicit safety filter.
- CodeQL and the labeler workflow are not part of the aggregate gate because CodeQL is skipped on
  these pull requests and labeling is not a build correctness check.
