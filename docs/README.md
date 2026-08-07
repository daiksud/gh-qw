---
type: index
title: "gh-qw documentation"
description: "Documentation for gh-qw repository paths, linked worktrees, commands, configuration, and development."
resource: gh-qw
tags: [gh-qw, documentation, index]
timestamp: 2026-08-05
template: splash
hero:
  title: "gh-qw"
  tagline: "A GitHub CLI extension for ghq-compatible repository paths and dedicated Git worktrees."
  image:
    file: ../.pages/src/assets/logo.svg
    alt: "gh-qw logo"
  actions:
    - text: Explore the concept
      link: /gh-qw/concept/
      icon: right-arrow
    - text: View on GitHub
      link: https://github.com/daiksud/gh-qw
      icon: external
      variant: minimal
---

`gh-qw` combines ghq-compatible repository paths with a dedicated root for linked Git worktrees.

<div class="docs-install">

```console
$ gh extension install daiksud/gh-qw
```

</div>

<div class="docs-card-grid">
  <a class="docs-card" href="/gh-qw/concept/">
    <strong>Concept</strong>
    <span>Understand the repository and worktree model behind gh-qw.</span>
    <span class="docs-card-arrow">Explore the concept</span>
  </a>
  <a class="docs-card" href="/gh-qw/reference/cli/">
    <strong>CLI reference</strong>
    <span>Find commands, flags, output contracts, and exit statuses.</span>
    <span class="docs-card-arrow">Browse the commands</span>
  </a>
  <a class="docs-card" href="/gh-qw/reference/configuration/">
    <strong>Configuration</strong>
    <span>Learn about roots, precedence, and the on-disk layout.</span>
    <span class="docs-card-arrow">Configure gh-qw</span>
  </a>
  <a class="docs-card" href="/gh-qw/development/">
    <strong>Development</strong>
    <span>Read contributor guidance and the Architecture Decision Records.</span>
    <span class="docs-card-arrow">Start contributing</span>
  </a>
</div>
