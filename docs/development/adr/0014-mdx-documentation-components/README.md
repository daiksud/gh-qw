---
type: adr
title: "ADR-0014: Use MDX components for the documentation homepage"
description: "Author the gh-qw documentation homepage with Astro components while preserving direct GitHub Markdown rendering."
resource: gh-qw
tags: [gh-qw, adr, adr-0014, astro, mdx, starlight]
timestamp: 2026-08-08
---

# ADR-0014: Use MDX components for the documentation homepage

- **Status:** Superseded by [ADR-0015](../0015-mdx-presentation-props/)
- **Date:** 2026-08-08
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0013](../0013-node-pnpm-viteplus-toolchain/)

## Context

The documentation homepage needs the visual structure of the Starlight splash page: a hero,
installation example, and navigation cards. The previous implementation expressed that structure
with raw HTML in the canonical `docs/README.md`. Raw HTML made the source harder to maintain and
duplicated presentation details that belong in the Astro site.

The canonical homepage is also viewed directly on GitHub.com. GitHub renders `.mdx` files as
Markdown but does not execute their JSX. It removes component tags and their attributes, while
rendering an MDX `import` statement as visible paragraph text. A normal MDX integration therefore
cannot put imports or component props in the source without degrading the GitHub view.

## Decision

The homepage will be authored as `docs/README.mdx` using the children-only Astro components
`Hero`, `Install`, `Cards`, and `Card`. Visible content remains Markdown children so GitHub renders
headings, links, paragraphs, and code blocks normally. The MDX source must not pass component props
or contain import statements.

The `.pages/src/plugins/mdx-auto-import.ts` satteri mdast plugin injects the component imports at
build time. It runs only for MDX documents and inserts the imports once per document. The Astro MDX
integration is registered after Starlight so its code-block integration order remains valid.

## Consequences

### Positive

- The homepage uses reusable Astro components instead of raw HTML presentation markup.
- GitHub.com renders the same source as an ordinary Markdown page without leaking imports or
  requiring JSX execution.
- The component boundary makes the hero, installation block, and card grid independently
  styleable and testable.

### Negative

- Homepage authors must keep all component content as children and cannot use props or explicit
  imports in `README.mdx`.
- The build contains a small custom MDX import-injection plugin because the existing satteri
  processor is incompatible with `astro-auto-import`.
- GitHub's component stripping means the visual component wrappers are intentionally absent from the
  direct source view.

### Neutral

- Only the homepage changes from Markdown to MDX; the other documentation pages remain
  `README.md` files.
- The content loader and link checker accept both `README.md` and `README.mdx` so the source
  extension does not change the generated route contract.
