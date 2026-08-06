---
type: adr
title: "ADR-0009: Add fzf-based interactive selection to list"
description: "Let list --fzf delegate interactive picking to the external fzf executable instead of gh-qw launching a shell."
resource: gh-qw
tags: [gh-qw, adr, adr-0009, cli, fzf, list]
timestamp: 2026-08-06
---

# ADR-0009: Add fzf-based interactive selection to list

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** gh-qw maintainers
- **Related:** [ADR-0006](../0006-command-set-v1/)

## Context

`gh qw list` prints canonical identities or paths for discovered repositories and, with
`--worktree`, their linked worktrees. As the number of repositories and worktrees grows, picking
one from that plain output becomes harder to do quickly. Many CLIs solve this by piping candidates
to the widely used external `fzf` fuzzy finder for interactive selection.

[ADR-0006](../0006-command-set-v1/) already decided that `gh-qw` will not add `look`, `--look`, or
any shell-launch behavior, precisely because spawning a shell from `gh-qw` creates portability and
shell-state problems; `gh-qw` outputs paths and leaves navigation to the caller's own shell. Adding
interactive selection must fit within that boundary rather than reopening it.

## Decision

`list` gains a `--fzf` flag. It filters candidates exactly like ordinary `list` (respecting
`<query>`, `-e`/`--exact`, and `--worktree`), then feeds their canonical identities, one per line,
to the external `fzf` executable resolved from `PATH` for a person to pick exactly one. `gh-qw`
never launches a shell itself: `fzf` renders its own interactive UI directly against the
controlling terminal, and `gh-qw` only writes the selected entry's absolute path to `stdout`,
exactly like `-p`/`--full-path` for a single entry. `--full-path` and `--unique` are accepted
alongside `--fzf` without error but have no effect, since `--fzf`'s output is always a path.

No candidates exits successfully without starting `fzf`. Canceling `fzf` (Esc or Ctrl-C) or `fzf`
finding no match for a typed query reuse `fzf`'s own documented exit statuses (130 and 1
respectively) with empty `stdout` and `stderr`, since `fzf`'s own screen already communicated the
outcome. Any other selection failure, including `fzf` missing from `PATH`, is an ordinary error.

A caller wires a small shell function to get the `cd` behavior, composing `gh-qw`'s path output
with their own shell exactly as ADR-0006 intended:

```sh
qwcd() { local dir; dir=$(gh qw list --fzf) || return; cd "$dir"; }
```

## Consequences

### Positive

- Interactive selection is available without `gh-qw` spawning a shell or owning any picker logic
  of its own; ADR-0006's boundary stays intact rather than being revisited.
- `fzf`'s own exit-status vocabulary (cancellation, no match) is reused directly instead of
  gh-qw inventing a parallel convention.

### Negative

- `--fzf` requires a separately installed `fzf` on `PATH`; without it, the flag fails with a clear
  runtime error instead of silently falling back to plain output.
- Getting the `cd` behavior itself still requires a caller-defined shell function; `gh-qw` does not
  ship or install one.

### Neutral

- A future interactive integration, if any, should follow this same shape — an external tool
  drives the interaction, and `gh-qw` only prints the outcome — rather than reopening ADR-0006.
