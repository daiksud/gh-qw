# gh-qw

A GitHub CLI extension for managing repositories and linked Git worktrees in
predictable directories. Git owns branches and worktree registrations; gh owns
GitHub network operations and authentication.

## Install

```sh
gh extension install daiksud/gh-qw
```

The complete command set requires Git 2.42.0 or newer and GitHub CLI 2.88.0 or
newer. Git must be on `PATH`; gh is resolved through `GH_PATH`, then `PATH`.
Configure authentication with `gh auth login` for network operations.

## Use

```sh
gh qw get cli/cli
gh qw list --full-path
cd ~/ghqw/github.com/cli/cli
gh qw worktree add -b feature/docs
gh qw worktree list
```

Main repositories live at `<root>/<host>/<owner>/<repo>`. Linked worktrees live
at `<worktree-root>/<host>/<owner>/<repo>/<slot>`. The default roots are `~/ghqw`
and `~/.local/share/ghqw/worktrees`, respectively.

Optional configuration in `~/.config/ghqw/config.toml`:

```toml
root = "~/ghqw"
worktree_root = "~/.local/share/ghqw/worktrees"
```

`GHQW_ROOT` and `GHQW_WORKTREE_ROOT` override those settings. XDG locations and
multiple roots are described in the [configuration reference](docs/configuration.md).

## Commands

| Command | Result |
| --- | --- |
| `get` (`clone`) | Clone or update repositories and print their paths. |
| `list` | List repository identities or paths; `--worktree` includes linked worktrees. |
| `root` | Print repository roots. |
| `rm` | Remove a selected repository or linked worktree after confirmation. |
| `migrate` | Move repositories into the primary root and repair their worktree registrations. |
| `worktree add` | Create a linked worktree at its deterministic slot. |
| `worktree list` | Inspect a repository's worktrees. |
| `worktree remove` | Remove a linked worktree; `--gone` selects absent upstreams. |
| `worktree prune` | Prune Git metadata and eligible managed directories. |

See the [CLI reference](docs/cli.md) for exact arguments, output, exit statuses,
and safety conditions. Documentation describes the current implementation.

[Architecture](docs/architecture.md) · [Contributing](docs/contributing.md) · [MIT license](LICENSE)
