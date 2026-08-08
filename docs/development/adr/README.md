---
type: index
title: "Architecture Decision Records"
description: "Index of Architecture Decision Records for gh-qw."
resource: gh-qw
tags: [gh-qw, adr, index]
timestamp: 2026-08-09
---

# Architecture Decision Records

This directory records significant architecture decisions for `gh-qw` as lightweight
Architecture Decision Records (ADRs). Each record captures the context, the decision, and its
consequences without duplicating implementation details or command reference material.

While `gh-qw` remains `0.y.z` and before `1.0.0` is deliberately declared, accepted ADRs may be
revised in place as the project establishes its policies. From `1.0.0` onward, accepted ADRs are
immutable except for corrections that do not change the decision, such as typo, link, or metadata
fixes. When a decision changes under that rule, add a new ADR, mark the old record as
`Superseded by` the new ADR, and link the two records. Do not rewrite an accepted decision to make
it appear that the new choice was always in effect.

## Adding an ADR

1. Copy the [`template/`](template/) directory to `NNNN-short-title/` using the next number.
2. Keep the record focused on one decision and use relative links for related ADRs.
3. Add its exact title and status to the index below.
4. While `gh-qw` is `0.y.z`, revise an accepted record in place when its decision changes. After
   `1.0.0`, supersede the record instead.

## Statuses

| Status | Meaning |
| --- | --- |
| `Proposed` | Under discussion and not yet in effect. |
| `Accepted` | Approved and in effect. |
| `Deprecated` | Retained for history but no longer recommended. |
| `Superseded` | Replaced by a linked, later ADR. |

## Index

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-record-architecture-decisions/) | Record architecture decisions | Accepted |
| [0002](0002-distribute-as-gh-cli-extension/) | Integrate distribution, network operations, and authentication with `gh` | Accepted |
| [0003](0003-go-cobra-prebuilt-binaries/) | Use Go, Cobra, prebuilt binaries, and controlled subprocess I/O | Accepted |
| [0004](0004-ghq-directory-convention/) | Adopt the ghq directory convention with paths as the source of truth | Accepted |
| [0005](0005-dedicated-worktree-root/) | Use a dedicated worktree root and `worktree` subcommand | Accepted |
| [0006](0006-command-set-v1/) | Define the v1 command set | Accepted |
| [0007](0007-configuration-sources/) | Configure gh-qw through its file and environment variables | Accepted |
| [0008](0008-conventional-commits-and-release-notes/) | Adopt Conventional Commits and automated release notes | Superseded by [0011](0011-type-named-release-labels/) |
| [0009](0009-interactive-selection-via-fzf/) | Add fzf-based interactive selection to list | Accepted |
| [0010](0010-public-api-and-semantic-versioning/) | Declare a public API and follow Semantic Versioning | Accepted |
| [0011](0011-type-named-release-labels/) | Name release labels after Conventional Commits types | Accepted |
| [0012](0012-github-settings-as-code/) | Manage repository settings as code with local-only apply | Accepted |
| [0013](0013-node-pnpm-viteplus-toolchain/) | Use Node, pnpm, and Vite+ for the documentation toolchain | Accepted |
| [0014](0014-mdx-documentation-components/) | Use MDX components for the documentation homepage | Superseded by [0015](0015-mdx-presentation-props/) |
| [0015](0015-mdx-presentation-props/) | Separate presentation-only props from reader-facing content | Accepted |
| [0016](0016-ubuntu-slim-runners/) | Use ubuntu-slim for lightweight GitHub Actions jobs | Accepted |
| [0017](0017-dependabot-ci-gate-and-auto-merge/) | Gate Dependabot merges on site validation | Accepted |
| [0018](0018-herdr-workspace-integration/) | Integrate worktree add/remove and rm with Herdr workspaces | Accepted |
