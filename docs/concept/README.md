---
type: concept
title: "The gh-qw concept"
description: "Why gh-qw combines ghq-compatible repository paths with a dedicated root for Git worktrees."
resource: gh-qw
tags: [gh-qw, ghq, git, worktree, design]
timestamp: 2026-08-05
---

# The gh-qw concept

`gh-qw` uses ghq-compatible repository paths and provides an explicit home for linked Git
worktrees. Everything else follows from keeping identity visible on disk and leaving repository
state to Git and `gh`.

## One layout, two roots

The default layout separates durable main worktrees from additional branch worktrees:

```text
~/ghqw/
└── github.example.com/acme/widget/                 # main worktree

~/.config/ghqw/
└── config.toml

~/.local/share/ghqw/
└── worktrees/
    └── github.example.com/acme/widget/
        └── feature/login/                          # linked worktree
```

The ghq convention `<host>/<owner>/<repo>` defines each repository identity. `~/ghqw` is
deliberately visible:
it contains ordinary clones that people enter, inspect, and use with standard Git tools.
Tool configuration and the dedicated worktree root live under the XDG Base Directory locations
`~/.config/ghqw` and `~/.local/share/ghqw` (or wherever `XDG_CONFIG_HOME`/`XDG_DATA_HOME`
relocate them), keeping secondary checkouts out of the main repository tree.

A main repository is presented as `<host>/<owner>/<repo>`. Worktrees are opt-in when listing
and are presented as `<host>/<owner>/<repo>@<branch>`. This keeps the repository as the primary
identity while making an additional checkout explicit.

## The path is the source of truth

`gh-qw` does not register repositories in a private catalog or write custom repository
metadata to mark a checkout as managed, adopted, healthy, or authoritative. A repository's
location determines its identity.

Git remains responsible for repositories, refs, and linked-worktree relationships. `gh`
provides the extension namespace, authenticated host context, and host API integration.
`gh-qw` coordinates those tools and derives predictable paths; it does not replace their
state models. A main checkout therefore remains a normal Git clone rather than a special
container that only `gh-qw` understands.

## Scope and name

`gh-qw` is Git-only, but it is not limited to `github.com`. The host is part of the path, so
repositories from any Git host—including GitHub Enterprise—fit the same layout.

The command name is `gh qw`: `q` points to the ghq convention, and `w` identifies
worktrees as the added concern. The short name describes the combination; it does not promise
drop-in compatibility with every ghq command or option.

## Scope boundaries

Compatibility is limited to the directory convention and familiar repository identifiers.
`gh-qw` does not include:

- `create`;
- `look`, `--look`, or any shell-launch behavior;
- bare repositories or `--bare`;
- multiple version-control systems or `--vcs`; or
- Git-config-backed settings such as `ghq.root`.

`list --fzf` composes with the external `fzf` executable for interactive selection, but it still
only ever prints the selected entry's absolute path; `gh-qw` itself never launches a shell or
changes directory (see [ADR-0009](../development/adr/0009-interactive-selection-via-fzf/)).
`worktree add`/`worktree remove`/`rm --herdr` compose with the external `herdr` executable the same
way, opening or closing a Herdr workspace without `gh-qw` ever owning a shell or that workspace's
UI itself (see [ADR-0018](../development/adr/0018-herdr-workspace-integration/)).

Normal settings belong to `gh-qw`'s configuration file or environment. Migration may inspect
ghq settings only to locate a source root; they do not become ongoing `gh-qw` configuration.

## Migration

`gh qw migrate` moves ordinary Git repositories from ghq source roots into the primary `gh-qw`
root while keeping their `<host>/<owner>/<repo>` identity. Non-Git and bare repositories are not
migrated, and linked-worktree relationships are repaired through Git rather than custom metadata.

This page defines those design boundaries. Exact commands, flags, configuration precedence,
and output contracts belong in the [CLI](../reference/cli/) and
[configuration](../reference/configuration/) references. The
[compatibility reference](../reference/compatibility/) records how current behavior matches ghq
or interoperates with `gh` and Git.
