# gh-qw

Spend less time thinking about working directories and more time on development.

gh-qw is a GitHub CLI extension for developers working across repositories and
parallel tasks. It gives repositories and linked Git worktrees predictable
locations, so starting, resuming, and finishing work does not depend on
remembering where each directory was placed.

## Why gh-qw?

- **Prepare a place to work:** clone to a predictable destination or register
  an existing checkout in place with `add`. Use `add --move` to relocate it,
  or `add --worktree` for parallel work.
- **Find the right place again:** list explicitly registered repositories,
  linked worktrees, or both, and pass their paths to other tools.
- **Clean up deliberately:** `forget` unregisters a location without removing
  its files; actual deletion remains separate and explicit.

Git owns branches and worktree registrations; gh owns GitHub network operations
and authentication. gh-qw adds consistent placement, discovery, and cleanup,
not a replacement for either tool.

[Purpose and scope](docs/purpose.md) defines the problem, user outcomes, and
criteria for deciding what belongs in gh-qw.

## Install

```sh
gh extension install daiksud/gh-qw
```

Prebuilt binaries are provided for macOS arm64 (Apple Silicon), Linux amd64,
and Windows amd64.

The complete command set requires Git 2.42.0 or newer and GitHub CLI 2.88.0 or
newer. Git must be on `PATH`; gh is resolved through `GH_PATH`, then `PATH`.
Configure authentication with `gh auth login` for network operations.

## Use

```sh
gh qw clone cli/cli
gh qw add ~/projects/my-api
gh qw list --full-path
gh qw add --worktree -R cli/cli -b feature/docs
gh qw list --all
```

Main repositories live at `<root>/<host>/<owner>/<repo>`. Linked worktrees
live at `<root>/worktrees/<unique-name>/<repo>`, with the default root `~/ghqw`.
The name combines `host-owner-repo-branch`, replacing punctuation (including
dots and slashes) with hyphens. If occupied, `-1`, `-2`, and so on are tried
automatically. The original Git branch name is unchanged.

Optional configuration in `~/.config/ghqw/config.toml`:

```toml
root = "~/ghqw"
```

`GHQW_ROOT` overrides `root`; worktrees always use the primary root's `worktrees`
subdirectory. Multiple roots and XDG locations are described in the
[configuration reference](docs/configuration.md).

## Commands

| Command | Result |
| --- | --- |
| `clone` | Acquire and register a new repository; existing checkouts use `add`. |
| `add` | Register a local repository; `--move` relocates it; `--worktree` adds a linked worktree. |
| `list` | List registered repositories; `--worktree` selects worktrees, `--gone` filters them, and `--all` includes both. |
| `forget` | Unregister a repository without deleting its files. |
| `root` | Print repository roots. |
| `remove` | Remove a repository, one linked worktree, or gone-upstream worktrees. |

Use native `git worktree prune` for stale Git worktree metadata; gh-qw does not
own a separate pruning policy.

See the [CLI reference](docs/cli.md) for exact arguments, output, exit statuses,
and safety conditions. Documentation describes the current implementation.

[Architecture](docs/architecture.md) · [Contributing](docs/contributing.md) · [MIT license](LICENSE)
