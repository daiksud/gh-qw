---
type: adr
title: "ADR-0015: Separate presentation-only props from reader-facing content"
description: "Define which MDX component inputs are safe for the GitHub-rendered documentation source."
resource: gh-qw
tags: [gh-qw, adr, adr-0015, astro, mdx, starlight]
timestamp: 2026-08-08
---

# ADR-0015: Separate presentation-only props from reader-facing content

- **Status:** Accepted
- **Date:** 2026-08-08
- **Deciders:** gh-qw maintainers
- **Supersedes:** [ADR-0014](../0014-mdx-documentation-components/)

## Context

The homepage is authored as MDX and is also rendered directly as Markdown on GitHub.com. GitHub
strips JSX component tags and their attributes, so a prop that contains reader-facing content
disappears from the direct Markdown view. At the same time, some component inputs only select a
visual arrangement and do not contain content that readers need to see.

ADR-0014 established children-only components for this compatibility constraint, but its blanket
prohibition on props prevents the Starlight-style `CardGrid` from exposing its presentation-only
`stagger` option.

## Decision

MDX components may accept presentation-only props that change styling or layout without carrying
reader-facing content. The homepage uses the boolean `stagger` prop on `CardGrid` for this purpose.

Props such as a card title, description, icon name, or navigation target remain prohibited because
their values must survive GitHub's component stripping. Reader-facing content must stay in Markdown
children, using `CardTitle` and `CardIcon` where a visual component boundary is needed. The source
must continue to omit explicit imports; `.pages/src/plugins/mdx-auto-import.ts` injects imports
during the Astro build.

## Consequences

### Positive

- Starlight's presentation-only grid behavior can be expressed without degrading GitHub rendering.
- Titles, icons, descriptions, and links remain visible as ordinary Markdown on GitHub.com.
- The rule gives component authors a clear boundary for future MDX inputs.

### Negative

- New props require a review of whether they contain reader-facing content rather than relying on
  their name or intended use.
- Content that would normally be a component prop must be written as children and may need a small
  wrapper component.

### Neutral

- `CardGrid` is the homepage grid component name; the former `Cards` name is no longer used.
- ADR-0014 remains in the record for the original children-only decision and is not rewritten.
