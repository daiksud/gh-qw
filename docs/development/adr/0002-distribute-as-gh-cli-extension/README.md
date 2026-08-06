---
type: adr
title: "ADR-0002: Integrate distribution, network operations, and authentication with `gh`"
description: "Distribute gh-qw as a GitHub CLI extension and use deterministic gh authentication for GitHub network operations."
resource: gh-qw
tags: [gh-qw, adr, adr-0002, github-cli, distribution, authentication]
timestamp: 2026-08-05
---

# ADR-0002: Integrate distribution, network operations, and authentication with `gh`

- **Status:** Accepted
- **Date:** 2026-08-05
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0003](../0003-go-cobra-prebuilt-binaries/), [ADR-0004](../0004-ghq-directory-convention/), [ADR-0006](../0006-command-set-v1/)

## Context

`gh-qw` organizes Git repositories and needs GitHub host context for repository resolution,
cloning, synchronization, and API lookups. Its users already rely on GitHub CLI for authentication
and terminal workflows. A standalone distribution or a separate network credential model would
duplicate installation, host, and authentication behavior.

Plain networked Git can use a credential helper unrelated to the accounts authenticated in `gh`.
Conversely, silently accepting whichever `gh` account is active makes multi-account behavior
ambiguous. Authentication must be selected deterministically before a network request begins.

## Decision

We will distribute the project as the GitHub CLI extension `gh-qw`, invoked as `gh qw`.
Installation and upgrades use the `gh extension` mechanism.

Every network-capable Git operation is delegated to `gh`: `get` clones with `gh repo clone`, `get
--update` synchronizes with `gh repo sync`, and `worktree add` uses `gh repo sync` when its
default-branch fallback needs network access. GitHub API requests use `gh` host and authentication
state. Local branch, revision, and worktree operations continue to use Git directly.
Consequently, `get` rejects `file://` input, while local discovery and `migrate` can still resolve
it.

Before a delegated operation or GitHub API request, account resolution follows this order:

1. explicit `GH_TOKEN` or `GITHUB_TOKEN`;
2. a valid cached login for `<host>/<owner>`;
3. exactly one authenticated login matching the owner case-insensitively;
4. the sole authenticated account for the host; or
5. an interactive selection from multiple accounts.

The selected login is cached and its token is injected only into the specific subprocess or API
call. Cache, account-listing, token, prompt, or ambiguity errors fail before network access.
There is no fallback to an ambient active account. Resolution is lazy: an existing repository
without `--update`, and a worktree operation satisfied entirely from local state, do not list
accounts or prompt.

## Consequences

### Positive

- Users get familiar installation, upgrade, command discovery, host, and authentication flows.
- The `gh qw` namespace clearly identifies the tool as part of a GitHub CLI workflow.
- Private repository access uses the explicitly resolved `gh` account.
- Ambiguous or broken authentication state fails before any network side effect.

### Negative

- Cloning and synchronization require an installed and authenticated GitHub CLI.
- Releases must follow GitHub CLI extension naming and asset conventions.
- `get` cannot clone `file://` repositories, and network operations are limited to GitHub hosts
  known to `gh`.
- Multi-account selection can require an interactive choice, and the account cache is additional
  local operational state.

### Neutral

- The extension remains a separate executable.
- Git and non-GitHub Git hosts remain usable where an operation is local.
