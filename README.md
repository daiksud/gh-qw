# gh-qw

`gh-qw` is a GitHub CLI extension that combines ghq-style repository management with
predictable Git worktree paths. Repository identity stays visible on disk, while Git remains
the source of truth for branches and worktree relationships.

`gh qw get` and `gh qw worktree add` clone and synchronize repositories through `gh repo clone`
and `gh repo sync`, so they require `gh` authentication and a `github.com` or
`gh`-authenticated GitHub Enterprise host. Account selection is deterministic: an explicit token,
a valid cached choice, an owner-matching account, a sole account, or an interactive choice must
identify the account before a network operation starts. Ambiguous or invalid authentication state
fails closed — see
[gh account selection](docs/reference/cli/README.md#gh-account-selection).

## Install

```console
gh extension install daiksud/gh-qw
```

## Layout

Main worktrees are ordinary Git clones. Linked worktrees use a separate root:

```text
~/ghqw/
└── github.com/acme/widget/                          # main worktree

~/.local/share/ghqw/worktrees/
└── github.com/acme/widget/feature/login/            # linked worktree
```

Each repository has the canonical identity `<host>/<owner>/<repo>`. A linked worktree is shown
as `<host>/<owner>/<repo>@<branch>`.

## Quick start

```console
$ gh qw get cli/cli
/Users/alice/ghqw/github.com/cli/cli

$ gh qw list
github.com/cli/cli

$ cd ~/ghqw/github.com/cli/cli
$ gh qw worktree add feature/docs
/Users/alice/.local/share/ghqw/worktrees/github.com/cli/cli/feature/docs

$ gh qw list --worktree
github.com/cli/cli
github.com/cli/cli@feature/docs
```

Pick an entry interactively with the external `fzf` and jump to it by wiring a small shell
function, since `gh-qw` itself only ever prints the selected path:

```sh
qwcd() { local dir; dir=$(gh qw list --fzf) || return; cd "$dir"; }
```

## Commands

| Command | Purpose |
| --- | --- |
| `gh qw get` / `gh qw clone` | Clone a missing repository or update an existing one. |
| `gh qw list` | List repositories and, with `--worktree`, linked worktrees; `--fzf` selects one interactively. |
| `gh qw root` | Print configured repository roots. |
| `gh qw rm` | Remove a repository or an `@branch` linked worktree safely. |
| `gh qw migrate` | Migrate GitHub repositories from a ghq layout. |
| `gh qw worktree add` | Create a linked worktree at its deterministic branch path. |
| `gh qw worktree list` | List one repository's registered worktrees. |
| `gh qw worktree remove` | Remove one managed linked worktree. |
| `gh qw worktree prune` | Prune stale Git metadata and proven orphaned paths. |

See the [CLI reference](docs/reference/cli/README.md) for every argument, flag, output format,
and exit status.

## Configuration

Optional settings live only in `$XDG_CONFIG_HOME/ghqw/config.toml` (or `~/.config/ghqw/config.toml`
when `XDG_CONFIG_HOME` is unset):

```toml
root = "~/ghqw"
worktree_root = "~/.local/share/ghqw/worktrees"
```

`GHQW_ROOT` and `GHQW_WORKTREE_ROOT` override these values. See the
[configuration reference](docs/reference/configuration/README.md) for precedence, multiple
roots, path safety, and ghq migration source discovery.

## Documentation

- [Concept](docs/concept/README.md)
- [CLI reference](docs/reference/cli/README.md)
- [Configuration reference](docs/reference/configuration/README.md)
- [Compatibility reference](docs/reference/compatibility/README.md)
- [Architecture decisions](docs/development/README.md)

## License

[MIT](LICENSE)
