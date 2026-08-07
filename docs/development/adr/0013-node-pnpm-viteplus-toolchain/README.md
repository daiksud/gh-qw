---
type: adr
title: "ADR-0013: Use Node, pnpm, and Vite+ for the documentation toolchain"
description: "Move the documentation site from a Bun-only runtime to Node, pnpm, and Vite+ while retaining Astro and Starlight."
resource: gh-qw
tags: [gh-qw, adr, adr-0013, astro, pnpm, vite-plus]
timestamp: 2026-08-07
---

# ADR-0013: Use Node, pnpm, and Vite+ for the documentation toolchain

- **Status:** Accepted
- **Date:** 2026-08-07
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0001](../0001-record-architecture-decisions/), [ADR-0012](../0012-github-settings-as-code/)

## Context

The documentation site used Bun as both its runtime and package manager. Dependabot was configured
with the `npm` ecosystem, which updated `.pages/package.json` but did not update `bun.lock`. The
resulting lockfile drift caused the frozen CI install to fail. Dependabot's separate `bun`
ecosystem supports `bun.lock` version updates but does not support security updates, while this
repository enables automated security fixes.

The site also needs a stable way to run Astro/Starlight, tests, and validation under Node. Vite+
is a beta unified toolchain that provides a task runner, Vitest, Oxlint, and Oxfmt, but it is not a
documentation-site generator and has no supported Astro configuration integration. Replacing
Astro/Starlight would therefore add unrelated migration risk.

Astro's current toolchain remains on TypeScript 6.x. TypeScript 7's native compiler does not yet
provide the programmatic API required by `astro check`, so upgrading TypeScript to 7.0.2 broke the
documentation check even though `astro build` itself still worked.

## Decision

The documentation site will:

- run on Node.js `24.19.0`;
- use pnpm `11.20.0`, pinned by `packageManager`;
- use Vite+ `0.2.8` for task execution, Vitest, Oxlint, and opt-in Oxfmt;
- retain Astro and Starlight as the documentation generator and theme;
- retain `astro check` and pin TypeScript to `6.0.3` until the upstream TypeScript programmatic API
  support tracked by [withastro/roadmap discussion 1321](https://github.com/withastro/roadmap/discussions/1321)
  is available; and
- configure `allowBuilds.esbuild: true` in `pnpm-workspace.yaml` so pnpm can run Astro's native
  build dependency.

Vite+ is used through `vp run` tasks named `site:dev`, `site:build`, and `site:preview`. The
`vite` package override that aliases Astro's Vite to Vite+ is deliberately not used because the
combination is not an officially supported Astro integration. The existing source tree is not
automatically reformatted; Oxfmt is available through the explicit format command.

Dependabot continues to use the `npm` YAML ecosystem value for this pnpm project, because that is
the value used by Dependabot for npm-compatible lockfiles including pnpm. Major TypeScript updates
are ignored until Astro's check tooling supports them. pnpm 11 is newer than Dependabot's currently
documented pnpm ceiling of v10, but pnpm 10 and 11 emit the same lockfile format (`9.0`); the next
Dependabot update must be verified to update `pnpm-lock.yaml` in practice.

## Consequences

### Positive

- Node, pnpm, and the lockfile are aligned with the dependency automation and CI runtime.
- Astro/Starlight's existing external `../docs/` source layout, content schema, sidebar, Mermaid
  integration, and mdast plugins remain unchanged.
- Vite+ supplies a single command surface for build tasks and tests without forcing an unverified
  Astro/Vite override.
- `astro check` continues to validate future `.astro` components.

### Negative

- Vite+ is beta and permits breaking changes in patch releases, so its version is fully pinned.
- pnpm 11 is outside Dependabot's currently documented supported range and must be monitored after
  the next update cycle.
- TypeScript must remain at 6.0.3 until the upstream language-tooling API gap is resolved.
- Existing source formatting is not yet Oxfmt-clean; formatting is opt-in rather than part of the
  blocking validation command to avoid an unrelated repository-wide diff.

### Neutral

- This decision changes the documentation toolchain only. It does not change the generated site's
  public URL, base path, canonical Markdown location, or Astro/Starlight output contract.
